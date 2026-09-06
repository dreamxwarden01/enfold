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

---

## 2026-09-04 — KDF chain pinned by test vectors, confirmed by a clean-room implementation

`testdata/kdf-vectors.json` is generated by `tools/kdfvec` from fixed, recognisable inputs and
records every intermediate value, so a future implementation can be diffed stage by stage rather
than only at `IK`.

**Independence was the point.** A second implementation was written from `FORMAT.md` §3 and §3.3
plus `testdata/kdf-inputs.json` alone, with no access to the generator, and matched the reference
on all 63 values. The primitives beneath were separately checked against published vectors — HKDF
against RFC 5869 (including the zero-length-salt case R1 depends on), Argon2 against the reference
implementation's own CLI outputs — in `internal/kdf/primitives_kat_test.go`.

**What the exercise changed in the spec.** The clean-room implementer reported every point at which
it had to guess. It guessed right each time, but a guess is a guess, so those points are now rules:
R13 names which of the two salts `salt'` is built from and where the standalone password slot's
Argon2 parameters come from; R5 now pins X25519 output as the raw u-coordinate, not only P-256;
R12 names the wrapping key. §6.3 gained a note that **ML-KEM decapsulation with the wrong key does
not fail — it returns a different valid-looking key** (implicit rejection), so the AEAD is the
only place that error can ever surface.

**A generator bug worth remembering.** ML-KEM encapsulation is randomised. The first version of
the generator called `Encapsulate()` on every run, so regenerating produced fresh ciphertexts and
silently changed `K_k`, `pre` and `IK` for both hybrid slots — the vectors were not reproducible,
which is the one property a vector file must have. The two ciphertexts are now pinned constants
and everyone, the generator included, decapsulates them. Two consecutive runs are byte-identical.

**Not yet done:** a NIST ACVP known-answer check of `crypto/mlkem` itself, and a Python
implementation for cross-language coverage. Both were in flight and were stopped when their build
activity kept tripping the anti-malware product on the development machine (below).

---

## 2026-09-04 — Anti-malware false positives: a dev workaround, and why it must stay a dev workaround

Kaspersky's cloud heuristics flagged the Go **test binary** as
`VHO:Trojan-Downloader.Win32.Convagent.gen` and deleted it at link time, and Defender did the same
when Kaspersky was paused. Ordinary `go run` binaries from the same toolchain were untouched; the
trigger is the symbol table and DWARF in test binaries — a documented Go false positive.

**Development:** `GOTMPDIR` is pinned to a fixed directory and test binaries are built stripped
(`-ldflags=-s -w`, `scripts/test.ps1`). Stripping avoided the heuristic; a folder exclusion alone
did not, because the cloud verdict overrides it.

**Release: none of that applies, and it must not.** Users cannot be asked to whitelist software.
The answer is Authenticode signing and vendor false-positive submission, now recorded in
`SCOPE.md` as a prerequisite for the first release. The test-binary workaround is not a security
decision and confers no security property; it exists so `go test` runs on one machine.

---

## 2026-09-04 — Correction: stripping does not avoid the false positive; the exclusion never matched

**Two things in the previous entry were wrong.**

**Stripping symbols was observed being flagged.** The one run that survived coincided with
protection being toggled; the anti-malware log afterwards shows stripped test binaries deleted at
link time twice more. `-s -w` stays in `scripts/test.ps1` because it shrinks the trigger surface,
but it is not the fix and must not be described as one.

**The folder exclusion never matched, and the reason is worth knowing beyond this project.** The
Claude desktop app is MSIX-packaged, so every process it spawns — `go.exe` included — has writes
to `%LOCALAPPDATA%` silently redirected to `%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Local\`.
The exclusion was added for the real path; the files were at the redirected one, which the
anti-malware product reported and which was easy to misread as the same directory. `GOTMPDIR` now
lives at `%USERPROFILE%\go\tmp`, outside AppData, where the real path and the visible path are the
same thing. The user spotted the path discrepancy in the log.

**Consequence for the product, not only the toolchain:** the keystore's default location is under
`%LOCALAPPDATA%`. A development build run from a Claude-spawned shell will write it to the
redirected location, invisible to the same binary launched normally. Recorded in `SCOPE.md`.

---

## 2026-09-04 — `GOTMPDIR` is `D:\MyPersonalProjects\go-tmp`

Supersedes the `%USERPROFILE%\go\tmp` location named in the previous entry. Both are outside
AppData and therefore free of the MSIX redirection; the workspace directory was chosen because it
is where the user will look for it, and because `GOTMPDIR` is a machine-wide Go setting, so the
name is deliberately project-neutral rather than `enfold-something`. `/go-tmp/` is also in
`.gitignore` in case it is ever pointed inside a repository.

---

## 2026-09-04 — `crypto/mlkem` ML-KEM-1024 verified against NIST ACVP; pre-freeze item 1 closed

Go 1.26's standard-library ML-KEM-1024 was checked against the NIST ACVP-Server FIPS 203 vectors
(`gen-val/json-files/ML-KEM-keyGen-FIPS203` at commit `15c0f3de`, sha256 `d7a62a2c…`;
`ML-KEM-encapDecap-FIPS203` at `ad33b3d9`, sha256 `a556952c…`; parameter set confirmed
ML-KEM-1024 in every group header):

| Operation | Cases | Result |
| --- | --- | --- |
| keyGen, seed → `ek` (public API) | 25 | pass |
| keyGen, seed → full 3168-byte `dk` incl. secret vector (stdlib internal hook) | 25 | pass |
| decapsulation, valid ciphertext (accept branch) | 5 | pass |
| decapsulation, modified ciphertext (**implicit-rejection branch**, `k = SHAKE256(z ‖ c)`) | 5 | pass — the rejection output was also computed independently in Python |
| encapsulation, derandomised | 25 | pass |
| determinism across two processes; rejection of seed lengths 0/32/63/65/96 | — | pass |
| **negative control**: one hex digit flipped in each KAT file | — | exactly the expected failures, so the comparisons are not vacuous |

**Two things learned.** ACVP ships only the *expanded* 3168-byte `dk` for decapsulation cases,
which the public API cannot load — it takes the 64-byte seed — so full decapsulation coverage
needed a `go test -overlay` into the stdlib package to reach the internal constructor. That route
is too fragile to keep. And the earlier assumption that encapsulation cannot be KAT-checked was
out of date: **Go 1.26 exports `crypto/mlkem/mlkemtest.Encapsulate1024(ek, random)`**, a
derandomised variant, so encapsulation *is* pinned.

**In the repository:** `testdata/mlkem-acvp-subset.json` (2 keyGen + 2 encapsulation cases, with
provenance) and `internal/kdf/mlkem_kat_test.go`, public API only. The full run and the overlay
test live under `D:\MyPersonalProjects\go-tmp\mlkem-kat\` and are reproducible from the notes
there.

**With this, SCOPE.md pre-freeze item 1 is complete on both axes:** every primitive in the chain
(HKDF, Argon2id, ML-KEM-1024; X25519, P-256 ECDH, AES-GCM and HMAC being stdlib primitives with
no project-specific configuration) is checked against a published authority, and the composition
is checked against a clean-room implementation.

---

## 2026-09-04 — UI layer: Wails v3; destroy-on-close-to-tray as the provisional default

**Trigger.** The user's Task Manager, the same day: two Electron chat applications idling at
1,415 MB (10 processes) and 1,000 MB (15 processes). Their constraint: an idle footprint around
100 MB is acceptable if the interface is good enough; 1 GB+ before the program has done anything
is not. That matters more here than for most applications, because this one is designed to sit
in the tray for hours (§10 session cache) — the UI baseline is a standing cost, unlike Argon2id's
512 MB–1 GB, which is transient and returned after unlock.

**A gap in this log.** The 2026-08-31 entry chose Go and named Wails v2 without recording that
Wails renders the UI as HTML in WebView2 — a web UI, which the user had objected to at the very
start of the project ("the most criticised way to build a Windows application"). That objection
was never answered in writing. It is answered below, by measurement rather than argument.

**Measured on the development machine** (Windows 11 26200, Go 1.26, every build
`CGO_ENABLED=0`, WebView2 runtime 152): each candidate as a throwaway shell, launched, left alone,
and the *private working set* summed over the whole descendant process tree — the figure Task
Manager's Memory column shows. Screenshots were reviewed by the user.

| Route | Shell content | Procs | Private WS |
| --- | --- | ---: | ---: |
| `lxn/walk` — native Win32 common controls | TreeView + virtual ListView, 5000 rows + tray icon | 1 | 4.1 MB |
| Gio v0.10.2 — self-drawn, Direct3D 11 | virtual list, 5000 rows | 1 | 75.7 MB |
| Wails v2.15.0, window visible | stock greeting page | 7 | 108.4 MB |
| Wails v2.15.0, window **hidden** 25 s | same | 7 | 106.7 MB |
| Wails v3.0.0-beta.16, window visible | stock greeting page | 7 | 127.3 MB |
| Wails v3.0.0-beta.16, window **closed**, tray only | — | 1 | **6.8 MB** |
| Wails v3.0.0-beta.16, window recreated from the tray | stock page | 7 | 130.2 MB |
| Wails v3.0.0-beta.16, closed again | — | 1 | 9.0 MB |

Two lines decide it. Hiding a Wails v2 window releases nothing — the WebView2 process group
stays resident, and v2 offers no way to destroy it short of quitting. Closing a Wails v3 window
destroys the group entirely and the process idles as a single Go process at WinRAR's size
(WinRAR idles at 5.4 MB on the same machine); recreating the window from the tray took 0.2 s
warm. The v3 probe also confirmed that a zero-window application keeps running with only a tray
icon (`WindowsOptions.DisableQuitOnLastWindowClosed`) and that close → recreate → close works
without incident. Probe sources, logs and the full research digest with sources are under
`D:\MyPersonalProjects\go-tmp\ui-probe\` (`RESULTS.md`, `research-dump.txt`), outside the
repository because they drag in three UI toolkits' dependencies.

**What the research established** (WebView2 claims adversarially verified against the live
sources; the toolkit claims spot-checked against the GitHub API):

- The WebView2 process group — one browser process, at least one renderer, GPU and utility
  processes — is an architectural floor, not a tunable: renderers cannot be shared because every
  WebView2 frame is DevTools-attached, and `--single-process` is "actively unsupported" per
  Microsoft. `TrySuspend` and `MemoryUsageTargetLevel` are best-effort and renderer-only. **Only
  `Close` reaches zero**, at a cold-start cost on re-show. The measurements match this exactly.
- `lxn/walk` upstream has been frozen since 2021-01-12 and has never had a release. The live
  lineage is **`github.com/tailscale/walk`** (+ `tailscale/win`): maintained by Tailscale
  engineers (commits 2026-07-02), used by Tailscale's own Windows client, CGO-free (the cgo
  message loop was deleted), with a virtual-mode `TableView`, `TreeView`, `WM_DROPFILES`,
  `NotifyIcon` with runtime icon changes and per-monitor-v2 DPI. Pseudo-versions only; the API
  has drifted from upstream.
- Gio has no file drop from Explorer (ticket open since 2020), no tray, no tree widget, and is
  system-DPI-aware only. Fyne needs a C compiler on Windows. `windigo` is single-maintainer,
  v0.x with a mid-2025 rewrite, no virtual list and no tray abstraction.
- Wails v3 has been in beta since 2026-08-02 after two and a half years of alpha; betas are cut
  nightly; the project calls the desktop API stable, says teams run it in production, and still
  calls v2 "the current stable release". v3's WebView2 binding is vendored in-tree and pure Go,
  so the whole stack stays `CGO_ENABLED=0` (confirmed with `go version -m` on the probe binary).
  File drop from Explorer, tray icon changes, click handlers and per-monitor DPI are all present.
- Windows 10: the WebView2 Evergreen runtime was rolled out to 1803+ Home/Pro in 2022–2023 and
  is installed by Microsoft 365 Apps since 2021, but Microsoft says "a small number" of Windows
  10 devices still lack it. Edge/WebView2 updates on Windows 10 22H2 are committed until at
  least October 2028 without ESU.

**Decision: Wails v3.** Pin a specific beta tag in `go.mod`; do not track nightly. The
application is a tray-resident process that creates its window on demand. The session cache
(KWK in `memguard`) lives in the Go process and is untouched by any of this; a user reopening
the window within the session timeout does not re-authenticate.

**Provisional, marked pending by the user: destroy on close-to-tray.** The window is destroyed
rather than hidden when it closes, which is what makes the 7–9 MB idle figure real. This stays
if reopening the *real* interface shows no noticeable delay, stutter or state loss; if it does,
the window is hidden instead and ~130 MB resident is accepted — in the user's words, a
trade-off, and the resident figure is not unacceptable. The v3 choice holds either way, since
v2 could not offer the option at all. Listed in SCOPE.md under "Deliberately unresolved".

**Why a web UI is now acceptable, against the original objection.** The objection to "web for
Windows" has three parts. Two are avoided: no bundled browser engine (the system WebView2 is
used; the binary is ~10 MB) and no Electron-class memory (the floor is ~130 MB with a window
open, and 7–9 MB idle in the tray if destroy stays, both measured). The third — controls that
are not native Windows controls — is accepted on purpose: Win32 common controls have no official
dark mode (7-Zip has none; WinRAR gained it only in 7.x), the user is a web developer and can
build and judge the interface directly, and the user's stated tolerance is ~100 MB *if the
interface is good*, which is exactly the trade a web UI makes.

**Rejected.** Wails v2 (cannot release the WebView; single window; no tray). Gio (75 MB, neither
native-looking nor web-flexible, missing file drop and tray). Fyne (CGO). windigo (API churn,
gaps). `lxn/walk` as such (frozen). **`tailscale/walk` is retained as the fallback**: the Go core
does not know which UI sits on top of it, so switching costs only the UI layer, and its 4 MB /
native-controls profile is the right answer if the WebView2 dependency or the ~130 MB open-window
baseline ever becomes unacceptable.

**Risks recorded.** v3 is beta software with nightly tags — mitigated by pinning, a thin UI layer,
and the walk fallback. The WebView2 runtime is a deployment dependency: the installer must check
the `pv` registry value and run Microsoft's ~2 MB bootstrapper when absent (SCOPE.md). Reopening
a destroyed window is a cold start of the WebView2 process group — 0.2 s measured warm with the
stock page; the real interface and a cold disk cache will cost more, which is precisely what the
pending decision above will be judged on.

**A new trap, discovered while looking at this.** WebView2 keeps a disk cache in its user-data
folder. Anything served to the WebView over loopback — the media-preview stream in SCOPE.md —
would be written to disk *decrypted* by the browser engine unless the response forbids it.
Recorded as DESIGN.md trap #13: preview responses carry `Cache-Control: no-store`, and the
WebView2 profile runs with caching disabled or in-private where the framework exposes it. If
destroy-on-close stays, it has a security side effect too: the renderer's copy of whatever
decrypted index data was on screen goes with it.

**Toolchain now installed** on the development machine: `wails3` CLI v3.0.0-beta.16 (also `wails`
v2.15.0 and `rsrc`, used only for the probes), Node 24.13 / npm 11.9.

---

## 2026-09-04 — Three questions before code: device identity, AppContainer, one key in slot 9d

Raised by the user just before implementation was to start. Two were answered from the existing
design plus verification; the third produced measurements on real hardware and a short list of
things to settle before the hardware-slot code is written. Research was run as Sonnet finders
with Opus verifiers (every claim re-fetched; two refuted on citation only); the local checks and
the YubiKey probes are reproducible from `D:\MyPersonalProjects\go-tmp\ui-probe\actest.ps1` and
`D:\MyPersonalProjects\go-tmp\piv-probe\` (`RESULTS.md`, `research2-dump.txt`).

### 1. "Device identity must persist, but we cannot write persistent keys into the TPM"

Already how `SYNC.md` §3.1 works, and confirmed as the same model Windows uses itself. The
identity key is created under the TPM's storage root key and comes back as a wrapped blob that
lives **in our keystore file**; it is loaded transiently to use and consumes no persistent TPM
storage. Windows' Platform Crypto Provider does exactly this (`.PCPKEY` blobs on disk, keys
loaded into the TPM on demand). The identity is persistent; nothing is written into the TPM. What
is lost on TPM clear, reinstall or board swap is the blob's usability, which §3.1 already handles
by re-pairing, never on the unlock path.

Two additions from the discussion: **a TTL on trust, distinct from identity** (recorded in
`SYNC.md` §8 — pin records could expire and be renewed by a re-confirmation from the phone); and
the unverified point that Microsoft documents nowhere whether the TPM 2.0 storage hierarchy is
left with empty authorization for applications. Practical consequence for later: prefer the PCP
KSP through `ncrypt.dll` (Microsoft's supported path, pure syscalls) or test go-tpm directly on
this machine (TPM 2.0 present, "Ready for storage"). Post-v1 either way.

### 2. "AppContainer isolates memory and AppData between apps of the same user"

**No — and the direction of the mistake matters.** Measured on the development machine: an
unelevated PowerShell running as the user opened all 11 running AppContainer processes (WebView2
renderers, SearchHost, ShellExperienceHost, LockApp, …) for `PROCESS_VM_READ` and read their
image headers; the ACL of a Packages isolated-storage folder grants the user `(F)`. Microsoft's
own words, verified: the integrity mechanism restricts lower-integrity subjects only, "preventing
information disclosure is not a goal", and it "is not intended as an application sandbox" —
AppContainer protects the system from the app. So it cannot replace device binding either, and
could not even in principle: device binding defends against the keystore being copied to another
machine, which no in-machine isolation addresses.

What the survey found instead, all verified against Microsoft Learn:

- **VBS enclaves** are the one mechanism Microsoft frames as protecting an app's secrets from a
  higher-privilege attacker on the same machine. Third parties can use them since Windows 11
  26100.2314, but the enclave must be MSVC-built against the enclave CRT and signed through
  Trusted Signing with enclave EKUs. Not reachable from Go. **PPL** is anti-malware-only (ELAM).
- **DPAPI does not isolate apps of the same user** — Microsoft: "all applications running under
  the same user can access any protected data that they know about" — and `LOCAL_MACHINE`
  scope gives "no real protection" on a workstation. `SYNC.md` §3.1's choice of machine scope
  already rests on the phone approving every request, not on DPAPI as a boundary. Also noted:
  the DPAPI prompt-struct flow is deprecated for removal in February 2027; the design never
  used it.
- What AppContainer *is* good for here: **containing our own parser.** An archive is untrusted
  input; a parser compromise inside an AppContainer child process cannot reach the network or
  the user's files. Recorded in `SCOPE.md` as a v2 candidate next to fuzzing.
- A partial mitigation that is real: holding the keys in a **service under a different account**
  would turn "unlocked = everything, forever" into "unlocked = what the attacker can make the
  service do while the session lasts". Deferred; noted in `DESIGN.md` §2.

`DESIGN.md` §2 previously called an AppContainer "the only architectural answer"; that sentence
was wrong and is corrected.

### 3. "Slot 9d holds one key — reuse across keystores? existing key? overwrite?"

**Reuse across keystores is the design, not an exception.** Every keystore stores its own
ephemeral `epk` and derives from ECDH against the same 9d private key; the token needs one key
for any number of keystores, as it does for any number of encrypted e-mails. Costs: `slot_pubkey`
is a cross-keystore linkable identifier; losing the token affects every keystore; rotating the
hardware key means re-enrolling in each.

**Measured on the user's YubiKey (firmware 5.7.4)** with a throwaway probe against slot 9d only;
the user typed every PIN into the probe's own prompt, never into the conversation:

| Fact | Measurement |
| --- | --- |
| Existing-key discovery | GET METADATA (firmware ≥ 5.3) gives algorithm, PIN/touch policy, origin and public key for every slot with no PIN, no touch, no certificate |
| Management key | Not the default: AES-256 (piv-go's 24-byte default was rejected with "expected 32"); the PIN-protected key in the PRINTED object worked |
| Generate P-256 in 9d, PIN once / touch always | 645 ms; attestation certificate available from F9 |
| ECDH on the token vs `crypto/ecdh` | identical in every round; 1.7–2.7 s including the human touch |
| **PIN-once state after our process exits** | **persists** — a later process did two ECDH rounds with no PIN |
| Why it later disappears | **Windows powers the card down 10.03 s after the last disconnect** (`SCardGetStatusChange` shows `UNPOWERED`); a VERIFY followed by idle → state gone at reconnect |
| Explicit `SCardDisconnect(SCARD_RESET_CARD)` | clears the state immediately; piv-go only ever uses `LEAVE_CARD` and `SHARE_EXCLUSIVE` |
| piv-go v2.6.0 | has GET METADATA, AES management keys, PIN-protected management key, attestation, retired slots; **lacks MOVE/DELETE KEY (0xF6)** and any reset |

**Verified from Yubico documentation** (firmware 5.7.4 tech manual, YubiKey SDK manual, ykman
docs): move/delete need 5.7.4 (yubico-piv-tool says 5.7.0 — treat 5.7.4 as the floor); GET
METADATA needs 5.3; retired slots 82–95 exist since 4.0 and accept generation; **PIN and touch
policy are fixed at generation**; defaults PIN 123456 / PUK 12345678 / management key
`0102…08` (3DES ≤ 5.6, AES-192 ≥ 5.7); retries 3/3; a blocked PUK has no unblock and forces a
PIV reset that wipes every slot; 5.7+ has PIN complexity that **rejects 123456 outright on the
Enhanced PIN series** and Unicode PINs counted in code points on an 8-byte wire field; the Bio
Multi-protocol Edition shares one PIN between FIDO2 and PIV with the PUK disabled; the Security
Key series and the Bio FIDO Edition have no PIV; FIPS keys need an 8-character PIN, forbid PIN
policy "never" and (140-3) 3DES management keys, and allow P-256; metadata's `origin` is
self-reported while the F9 attestation is signed; PIV works over NFC with touch inside 15 s;
firmware 5.8 changes nothing in PIV.

**Settled now** (small, and independent of the open policy):

- **Never overwrite an occupied slot by default.** If 9d holds a P-256 key with acceptable
  policies, reuse it and say so; if it holds RSA, P-384, a key with `touch=never`, or a key whose
  public key cannot be read (firmware < 5.3 without a certificate), generate ours in a retired
  slot 82–95 instead. Overwriting is an explicit, named, second-confirmed action on one slot.
- **Never call PIV reset**, and never probe with the default PIN. Show retries before asking.
- **Management key flow:** PIN-protected key first, then ask the user for theirs; if the default
  works, warn. Read the 9b algorithm from metadata (FIPS 140-3 keys refuse 3DES).
- **Rotation of the hardware key cannot depend on MOVE KEY** (piv-go lacks it): the new key goes
  into a fresh slot, keystores are re-wrapped, the old slot stays until the user deletes it with
  ykman. `DESIGN.md`'s earlier "retired slots hold superseded keys" wording is superseded by this.
- **UI order: PIN, then touch.** The token asks for the touch only after the PIN is accepted and
  the user is otherwise left waiting; recorded in `DESIGN.md` §10.

**Open, deliberately** (`SCOPE.md`, "Deliberately unresolved"): the PIN policy and token session
semantics — how long a verification stands, whether every operation needs a touch, whether the
app holds the exclusive connection for the session (which locks every other process out of the
card) or resets on every disconnect (which closes the 10 s window). The user asked for these to be
decided on the unlock UX once it exists, not on paper. Also open: **a `piv_slot` (u8) and
`token_serial` (u32) field in the hardware slot record**, needed if our key may live outside 9d
and useful for matching the inserted token before prompting; proposed, not yet added to
`FORMAT.md`. The format is not frozen, so the cost is the same now or later, as long as it lands
before the hardware-slot code.

**State left on the user's token:** slot 9d now holds the probe's P-256 key (no certificate,
invisible to Windows' smart-card stack). It stays until the user deletes it (`ykman piv keys
delete 9d`, firmware 5.7.4) or the product replaces it.

---

## 2026-09-04 — Decided: least privilege over a service split; dual-signed identity; PIN session deferred

The user's rulings on the three questions above, after the assessment.

**1. No service split. Least privilege.** The only Windows mechanism that isolates one user's
programs from each other is a different account, which for us means the key-holding core running
as a Windows service — and that means asking for administrator rights at install. Rejected: the
application does not ask for rights it does not need, and the same-user limitation is accepted as
Windows' own (`DESIGN.md` §2 now says so). What remains of the idea is free: the core/UI
boundary is message-shaped from the first line of code, with `KWK` and session state on the
core side only, so the option is not foreclosed.

**2. Identity: a device key *and* a keystore-resident key, co-signing.** The proposal to move the
identity out of the keystore into device-local storage as a single key was not adopted. The user's
model: the keystore keeps its own key — never synchronised, allowed into a full *local* backup
alongside the slots, useless off this machine because it is TPM/DPAPI-wrapped — and that key
signs together with the device identity to prove **"this keystore on this computer"**. The
reasoning is that trusting a device identity and then shipping the whole keystore to it is a
persistent, high-trust state, and a second factor bound to the keystore itself is worth its cost.
Trust carries a TTL and a level separate from identity, and expiry means the full pairing
ceremony. Direction only; the details are designed together with the sync feature
(`SYNC.md` §8).

**3. PIN session: the boundary is user participation, not session length.** The user's precise
formulation: a touch per operation *instead of* a PIN per operation is acceptable, and the card's
PIN-verified state need not expire at once — **what must never happen is the card releasing a key
with no user involved, turning it into an oracle for malware.** Touch policy *always* is that
guarantee, and it holds regardless of how long the PIN state lives; the 10 s power-down is
therefore a nuisance that forces re-entering the PIN, not a safety mechanism, and the earlier
"unsustainable" wording overstated it. The concrete scheme — hold the exclusive connection,
reset on release, re-prompt on power-down, or something else — is deferred until the unlock UX
exists (`SCOPE.md`).

**Implementation starts now**, bottom-up as agreed: `internal/format` (keystore and archive
codecs with fuzz targets), then `internal/kdf`, `internal/stream`, PIV, UI last. The hardware
slot record is implemented as specified today; the proposed `piv_slot` / `token_serial` fields
stay an open item and cost the same to add any time before the format is frozen.

---

## 2026-09-04 — `internal/format` landed; what the first review found

The package encodes and decodes both file types per `FORMAT.md`, with a fuzz target per decoder.
Writing it pinned five places the prose left open (`FORMAT.md` §3.4, R14–R18). A three-lens
review — specification conformance, parser robustness under hostile input, Go quality; Opus,
process kept out of the main context — then found what a first draft finds, and the fixes pinned
three more (R19–R21). The ones worth remembering:

- **A byte inside the AAD range that the AAD did not cover.** The decoder discarded `key_source`
  for software slots and the encoder wrote zero, so the AAD — computed from the struct — no
  longer matched the bytes on disk for exactly one byte, and that byte could be changed without
  an authentication failure. The reviewer found it by flipping every byte of a record and asking
  whether the recomputed AAD moved. That probe is now a permanent test, and decoding is
  canonical by rule (R21): every accepted byte is represented, and the fuzz targets assert
  byte-exact round trips.
- **`slot_state = 0` short-circuited every check**, so an unknown type, curve or flag rode in on
  an "empty" record. Empty records must now be entirely zero.
- **Three fuzz targets could never pass their own checksum**, so they only ever exercised the
  reject path. They now let the fuzzer own the body and add the checksum themselves.
- **No upper bounds** on `registry_len`, `index_len` or `freemap_len`, and chunk arithmetic that
  wrapped near 2^64 (R19). **No rules for file names**, so `../x` decoded cleanly (R20).
- Smaller: `ErrTruncated` and `ErrTrailing` were not `ErrInvalid` and carried no offset; the
  superblock pickers returned a bare int and swallowed the damaged copy's error; `pack_id` was
  round-tripped instead of treated as reserved; duplicate KIDs were accepted; a size assertion
  discarded its error.

Nothing found was a wire-layout defect — both the conformance and the quality reviewer checked
every field table against the encoders and found none — which is the part of the format that
would have been expensive to fix later.

---

## 2026-09-04 — `internal/kdf` landed; a reviewer found the missing Argon2 ceiling by detonating it

The package implements every derivation of `FORMAT.md` §3 and §3.3 — hardware slot with and
without the entangled password, both hybrid software slots end to end (seeds, key pairs, offline
wrap, unlock, the §6.3 verifier), the recovery-key digit encoding, the subordinate keys below
the VMK and the archive key, and the AES-256-GCM wraps of the VMK and of 32-byte keys. All 63
pinned values in `testdata/kdf-vectors.json` reproduce, including the 512 MiB Argon2id anchor;
`tools/kdfvec` stays as the independent reference that produced them. Three more rules were
pinned on the way (`FORMAT.md` R22–R24): the AADs for the three key wraps, tolerant recovery-key
input, and Argon2id parameter bounds.

**The incident.** The three-lens review workflow that had worked for `internal/format` was run
again. The cryptographic-misuse reviewer was asked "who bounds `argon2_m` — is a hostile slot
record a denial of service?", and answered by writing a probe that called Argon2id with
`m = 0xFFFFFFFF` KiB — 4 TiB — and running it. It reached 4.8 GB resident before the machine
froze; Claude Code itself died with `0xC0000409`, and the user rebooted. The finding was real:
`format.SlotRecord.Validate` enforced only the lower bounds, so a record could demand any
amount of memory *before* anything was authenticated (the parameters are in the AAD, but the
AAD is checked only after Argon2id has run). Now R24: `m` ≤ 2 GiB, `t` ≤ 32, `p` ≤ 32, checked by
the format layer on read and again by `kdf.Argon2Params.Validate` before any derivation; slots
that do not run Argon2id carry all three as zero.

The reviewers never delivered their reports — the process died under them — but their scratch
work showed what they were testing, and each of those probes became a fix: the VMK wrap took a
caller-supplied nonce, so a rotation that kept the record's old nonce under an unchanged IK
would have been textbook GCM nonce reuse — **wraps now draw their own nonce and return it**;
`salt` and `slot_salt` were both bare `[32]byte`, so R13's confusion was one typo away —
**they are distinct types now**; the exported HKDF and Argon2id could panic on absurd
arguments — **unexported, reachable only through validated paths**; the subordinate-key
derivations took `[]byte` and accepted a 7-byte "VMK" — **fixed-size secrets are arrays now**;
`ParseRecoveryDigits` rejected the en dashes and ideographic spaces a document paste carries —
**any Unicode space or dash is ignored (R23)**; and every output key in the vector file is now
asserted, not most of them.

**The rule that comes out of it**, recorded in the working memory as well as here: an agent that
may execute code is told, in its prompt, never to run a demonstration above 256 MiB or 30 s,
never to fuzz or benchmark, to run test suites with `-short`, and to prove denial-of-service
claims from the code and the library source. The follow-up review of this package ran under
those limits — and found the most important thing in this entry.

**A design gap, one layer before the code that would have had it.** VMK rotation re-wraps the
new VMK into every slot using the public keys *stored in the slot region*, with no credential
present — the property `DECISIONS.md` 2026-08-31 chose asymmetric slots for. The slot region is
checksummed, not authenticated, and a record's AAD is verified only when that slot unlocks the
vault. So an attacker with write access to the keystore file substitutes their own
`slot_pubkey`/`mlkem_ek` into, say, the recovery slot, and the next rotation — pre-selected on
every slot change — hands them the new VMK. Splicing and Argon2 downgrade had been analysed;
public-key substitution had not, by anyone, across two months of design and two reviews.
**Fix (`FORMAT.md` R25, `DESIGN.md` trap 15): the registry plaintext carries a SHA-256 of the
slot region as written**, authenticated by the registry AEAD under the Metadata key, which is
available exactly when a rotation runs. The reader verifies it after every unlock; a mismatch is
reported as slot-region tampering and blocks every rotation, re-wrap and slot mutation. It also
promotes the §6.2 splice from "caught after the next rotation" to "caught at the next unlock".
Smaller findings from the same pass, all fixed: recovery-key parse errors echoed five correct
digits of key material; `HardwarePre` inferred "no password" from a nil slice against R4's
explicit rule, so a skipped prompt could degrade to the token-only derivation — split into
`HardwarePreToken` and `HardwarePreEntangled`; two HKDF outputs left unzeroed; a comment that
claimed the X25519 ephemeral was wiped when `crypto/ecdh` offers no way to; Argon2 bounded in
memory but not work (R24 now caps `m × t`); and P-256 public keys on a slot record were never
checked to be on the curve — they are now, at parse time.

---

## 2026-09-05 — Compression knobs, and two RAR features promoted to 1.1

The user asked what zstd offers against RAR's dials (dictionary size, volumes, speed) and what a
recovery record is actually worth. Checked against the library we will use, not the reference
implementation: `klauspost/compress` v1.19.2, the only CGO-free zstd in Go.

**What we can actually expose.** Four speed presets (Fastest ≈ zstd 1–2, Default ≈ 3, Better ≈
7–8 at 2–3× the CPU, Best), not 22 levels. A match window — what RAR calls "dictionary size" —
from 1 KiB to 512 MiB, power of two, default 4–8 MiB by level; bigger costs memory on both sides
and time on the writer. Real dictionaries for many small files, built by `BuildDict` from
samples the caller picks (the library does not do COVER training). Block-level concurrency
inside a stream. Padding to a multiple of n bytes, which blunts the compressed-size side channel.
And on the decoder, the two limits that matter for untrusted input: `WithDecoderMaxWindow` and
`WithDecoderMaxMemory` — recorded as `DESIGN.md` trap 16, to be applied when the compression
layer is written; nothing decodes zstd yet.

**What does not map.** Solid compression: incompatible with per-file DEKs and in-place editing,
by design; the substitute for many small files is the trained dictionary now and small-file
packs later. Random access inside a compressed file: none, which SCOPE already accepted by
rejecting seekable zstd — video is stored raw, and the STREAM chunks give random access there.

**Volumes → committed for 1.1.** The user's ruling: large archives must be transferable and
backable-up online, so volumes are not optional; 1.0 can ship without them, 1.1 cannot. They
will be an export form — split bytes with per-part headers and hashes, immutable, reassembled by
concatenation — so the live format needs nothing now. `SCOPE.md` carries the plan.

**Recovery record → committed for 1.1, form and default open.** What was verified about RAR:
RAR5's record is Reed–Solomon, sized as a percentage (3% when not specified, at most 100%), and
"N% recovery record can repair up to N% of continuously damaged data" while handling scattered
damage far better than RAR4; repair is an explicit *Repair* command that writes
`fixed.<name>.rar`, never something extraction does on its own; an archive with no record can at
best recover its undamaged files. That matches the user's experience that damaged RARs simply
fail: the record is opt-in and repair is a separate step most people never run. For us the
design falls out cleanly: parity over **ciphertext**, so repair is keyless; damage location is
already exact because every 64 KiB chunk carries a GCM tag; overhead is the chosen fraction; and
it lives beside the archive (sidecar, or per volume) so the live format is untouched. The
library would be `klauspost/reedsolomon`, pure Go with assembly, same author as our zstd.

---

## 2026-09-05 — internal/stream: the STREAM layer, R26, and an EOF that had to be earned

**What landed.** `internal/stream`: `Writer` and `Reader` for §12's chunked AES-256-GCM, built on
`format.ChunkNonce`/`ChunkAAD`, with `PlaintextLen` as the exact inverse of `format.RawStoredSize`.
The Writer buffers one chunk and seals it only once it knows whether more follows, so the blob
never depends on how the caller split its writes, and a plaintext that is a multiple of 64 KiB
ends in a full chunk marked final. The Reader takes an `io.ReaderAt` and the blob's length and
implements `io.ReadSeeker`, which is what `http.ServeContent` needs for the loopback preview
server; a seek costs at most one re-decrypted chunk.

**Length-driven, not trial-driven.** age's reader learns which chunk is final by reading ahead to
EOF and, for a full last chunk, by trying the non-final nonce and then the final one. We have
something age does not: `stored_size` in an index the archive layer has already authenticated.
So the Reader derives the framing from the length, refuses any length that no canonical encoding
produces — including the "full chunk, then an empty final chunk" that age v1.0.0 emitted — and
never opens a chunk under two nonces. The final flag stays as the independent check: a blob cut
at a chunk boundary has a perfectly canonical length and is caught only because its last chunk
was sealed non-final. Pinned as **R26**. The counter bound of 2^32 chunks turns out to be exactly
R19's 2^48 bytes and NIST SP 800-38D's invocation limit — a coincidence worth writing down, not a
design.

**The bug the tests found first.** An empty file is one empty final chunk: 16 bytes of tag and
nothing else. The first Reader never opened it — with nothing to return, `Read` reported EOF
without touching the chunk, so any 16 bytes were an "authentic" empty file. Harmless for
confidentiality, wrong in principle, and a crack in the "everything authenticates" story. The fix
is an invariant, not a special case: **`io.EOF` is only ever returned after the final chunk has
opened**, which also covers a Seek past the end. `TestEOFAuthenticatesFinalChunk` pins it.

**Errors are not sticky in the Reader.** Each Read decides from the position; a chunk that failed
fails again. That lets a salvage tool seek around a damaged chunk, and it is what
`ServeContent`'s sniff-then-rewind needs. The cost lands on the caller: an error after 300 KiB of
authentic plaintext means "discard what you built", written down as `DESIGN.md` trap 17 —
extraction goes through a temporary file and a rename.

**Review.** Three Opus reviewers (spec conformance / hostile input / Go quality and crypto
misuse), 21 findings, one adversarial verifier per finding: 8 confirmed, all minor or nit, 11
refuted. Fixed: §11's `orig_size` row still said the plaintext length tells the reader which
chunk is final — false for compressed files, where the blob covers compressed bytes, and the one
finding that could have misled the archive layer; the §12 bullet "the last chunk is short" and a
`DESIGN.md` bullet from before R26 saying the same; a hostile `io.ReaderAt` returning a negative
count was treated as a full read (fail-closed regardless, since stale bytes cannot authenticate,
but now `ErrTruncated`); the chunk-count formula lived in two places with the Reader's slice
bounds depending on both agreeing; `TestSeek` would have panicked rather than failed on a
past-the-end draw the seed never produced; a fuzz seed built on a zero `testing.T`. Also fixed
from the read-through: `DESIGN.md` §8 still said "big-endian" — `FORMAT.md` §1 has said
little-endian since the byte-order decision, and the code was already right. Verified in GOROOT
while checking a refuted finding: `ServeContent` answers a multi-range request from a goroutine
of its own that can outlive the handler, so the preview layer must not Close the Reader on
handler return; noted on the type for when that layer is written.

**Not built, on purpose.** No unknown-length mode: every blob's length is in the index, and a
reader that trusts the frame to say where it ends is what trap 16 warns about for zstd. No
`ReadAt` on the Reader: the one-chunk cache is not safe for parallel calls, and a preview server
that wants concurrency opens one Reader per request.

---

## 2026-09-05 — internal/compress: the zstd layer, R27, and a window bounded by the record

**What landed.** `internal/compress`: `Params` (four presets, window, dictionary, concurrency,
padding), `Writer` and `Reader` reusable through `Reset`, `Probe` for DESIGN §9's sampling policy,
and `BuildDict`/`DictID`. Library: `klauspost/compress` **v1.20.0**, not the v1.19.2 the docs had
named: the zstd Go sources are identical between the two, only regenerated assembly differs, and
1.19.2 carries three dictionary fixes (BuildDict offsets, zero-literal corpora, a registered
dictionary dropped when decoding past the window) that this layer needs. Verified by diffing the
two module trees, not from release notes. `format.Index` gained the dictionary bound and checks.

**The reader is held to the record (R27).** Before decoding a byte it parses the frame header with
the library's `zstd.Header` and checks it against the index: dictionary ID against `storage`,
declared content size against `orig_size`, window against a limit derived from `orig_size`. It
hands the decoder exactly one frame — a block-header walker finds the end without decompressing —
and afterwards accepts only skippable padding frames. Output must be exactly `orig_size` bytes.
Every limit comes from the index or from a constant; nothing in the frame sizes an allocation.

**Trap 16 was wrong as written, and the fix is better than the doc's intent.** The trap said to set
the library's `WithDecoderMaxMemory` to `orig_size`. The review checked: in streaming mode that
option is a *window* cap, not an output cap, and since every streaming header's window is the
power of two *above* the content, the prescription would reject every frame this program writes —
an implementer following the doc would have shipped green tests and unreadable archives. What
actually bounds the decoder's allocation by the record is a rule the reviewers proposed and the
writer already satisfies: **a frame's window may not exceed the smallest power of two above
`orig_size`** — content cannot reference further back than it is long, and the library's own
header window for a declared size is exactly that. Consequences, all pinned in R27: the writer
always declares the size (there is no unknown-length mode), an empty file is stored raw (its
frame would have nothing to bound its window by), and a 10-byte frame for a 10-byte file costs
the decoder a 1 KiB window rather than the 513 MiB a hostile header could otherwise demand. 512 MiB stays
as the format's ceiling, pinned in the code rather than aliased from the library.

**Probe: median, not mean.** Three samples at start, middle and two thirds, as §9 says — but §9
did not say how to combine them. The mean lets one compressible header vote a file of images into
zstd (0.35, 1.0, 1.0 average to 0.78, "compress"); the median says 1.0, raw, which is the PDF case
§9 was written for. The mirror case — a compressible middle between incompressible thirds — is
stored raw and loses space only. Files up to three samples long are compressed whole.

**Two things the fuzzer and the review found in the library's contract.** `zstd.BuildDict` panics
(a negative slice bound in its history buffer) on any training sample longer than about 146 KiB;
training samples are now truncated to 128 KiB, which the arithmetic shows is always safe, while
the whole sample stays eligible as content. And a dictionary whose content contains a training
sample verbatim yields "0 literals" — the trainer needs bytes that do *not* match — so content is
taken from every other sample and the rest train the tables. A ten-second fuzz run also caught the
frame walker skipping three bytes of padding that sat inside the 17-byte header read-ahead.

**Review.** First round: three Opus reviewers, 36 findings, 15 confirmed (one blocker, the
BuildDict panic; one major, trap 16), 3 refuted, 18 left unverified when the session limit hit.
Those were triaged by hand, and nearly all were real or worth doing: the frame walker, the
size-bound window, goroutine leaks from a parallel encoder on a failed stream (every failure
path now resets the encoder, and `Release` is mandatory for `Concurrency` above 1),
`Window × Concurrency` memory (documented, and bounded at a 32 MiB window), `MaxEncodedSize`
rounded up to the padding, the probe encoder pooled instead of shared, and a run of doc and test
corrections. Second round, on the reworked reader: two reviewers, 17 findings, 7 verified
and all confirmed, none refuted. One blocker among them, and a good one: the library's
`zstd.HeaderMaxSize` is 17 because it counts the spec's 14-byte frame header without the 4-byte
magic, so a read-ahead sized from it could not hold the largest legal header — 18 bytes, a
dictionary ID of 65536 or more together with a content size of 4 GiB or more — and the Reader
would have called a frame its own Writer had produced corrupt. The read-ahead is now sized from
the format, 21 bytes, with the library's constant deliberately not used. The rest: the padding
bound was one step short for paddings under 8 bytes, single-segment frames were held to the
window rule only implicitly, a source that never progresses could spin the reader, and the
one-sample dictionary case could never succeed.

**Not built, on purpose.** No one-shot `Compress`/`Decompress` helpers: a Writer or Reader reset
per file is the same cost with one API. No COVER training: content selection is round-robin over
the heads of every other sample, and a better selector needs no format change because the
dictionary is just bytes in the index.

---

## 2026-09-05 — internal/keystore: unlock, rotation, the commit protocol, and three rules the code forced out of the spec

**What landed.** `internal/keystore`: `Open`/`Create`, `Unlock` with a standalone password, the
recovery key, or a token plus its entangled password, an `Unlocked` that holds the VMK for slot
mutations (`AddSlot`, `RemoveSlot`, `Rotate`, `RewrapStale`, `Export`), and a `Session` with the
three cached keys of DESIGN §10 and a transactional `UpdateRegistry`. A token is an interface of
two methods — its public key and one ECDH — so the PIV layer plugs in later and the tests use a
software P-256 key. Every change lands in one superblock flip: slot region into the inactive
copy, registry into a location the live one does not occupy, sync, inactive superblock at
seq + 1, sync, then the file is trimmed to the live registry's end. A fresh file writes copy A at
seq 1 and copy B at seq 0, since the format reader treats a tie as corruption.

**Three things the spec did not say, or said wrongly, that writing the code exposed.**

*The invariant cannot see passwords.* §6.4 evaluates required-secret sets, and the table in
DESIGN §5 distinguishes "two YubiKeys sharing one entangled password" from two with different
ones — but the file stores no password material, so the code cannot tell the cases apart. It
counts every entangled password as one secret (DESIGN §5, implementation note). Conservative:
two tokens each with its own password still need a recovery slot, which is what the design wants
present anyway.

*`rewrap_stale` cannot be in the AAD.* §8 says a slot a rotation cannot reach is marked
`rewrap_stale` with its `wrapped_vmk` left exactly as it is; R14 says the AAD covers every byte of
the record including `flags`. Both cannot hold: setting the bit re-keys the AAD, and the rotation
that sets it is the one without the slot's secret. The first deferred rotation the tests ran
produced a slot that never opened again. **R29**: the bit is cleared when the AAD is computed —
it is a hint for the UI and bookkeeping behind `rotation_pending`; the authenticated statement of
staleness is the generation inside `wrapped_vmk`, which an unlock checks regardless. Changed in
`internal/format`, with a test that the bit is on the wire but not in the AAD and every other
flag bit is in both.

*"Only the entangled password, which the user just typed" is true for one slot.* DESIGN §5's
promise that rotation needs no credential present holds for the slot that opened the vault and
for every slot without a password. For a second hardware slot with an entangled password it holds
only if that password is the same one — and the file cannot check a password without that
slot's token, so re-wrapping with the typed password would silently *replace* the other slot's
password. The first draft did exactly that, and the test that expected a stale slot got a
re-keyed one. **R30**: `Rotate` re-wraps other entangled slots only with
`RotateOptions{SharedPassword: true}`, otherwise leaves them stale; `RewrapStale` takes the stale
slot's own credential and verifies it against the slot's existing wrap — the previous VMK, which
is what it still holds — before wrapping the current VMK in. DESIGN trap 18 records the
companion bug: a re-wrap that replaced `epk` before discovering it had no password left the slot
unopenable; records are now built in a copy and assigned whole.

**Export is a keystore file (R28).** §15 said what an export carries; nothing said its form.
Making it the same file format with only the recovery slots in the slot region answers the
verifiability requirement for free: opening it with the recovery key *is* the test, and restoring
is opening it and enrolling new slots.

**Also.** `kdf.WrapVMKWithNonce` is exported: the slot AAD covers `wrap_nonce`, so the nonce must
be drawn and placed in the record before the AAD exists, and the wrap-draws-its-own-nonce API of
`internal/kdf` cannot be used for slots. The registry's tag is stored both after the ciphertext
and in the superblock, and a reader requires them to agree.

**Review.** Three Opus reviewers (spec conformance / hostile input and crashes / crypto and Go
quality), 32 findings, 26 verified: 18 confirmed, 8 refuted, 6 nits judged by hand. The blocker
was found by two lenses independently: a registry-only commit recomputed `slot_region_hash` over
the live region as read from disk, so the first routine registry update after a detected
mismatch — adding an archive — authenticated the tampered region and destroyed the evidence, and
the next rotation would have handed the new VMK to the substituted key. The registry now carries
its authenticated hash forward and recomputes it only when the slot region is actually written;
R25 records the converse rule, and the tamper test reopens the file after an update. Two majors
on handle lifetimes: `Unlocked` and `Session` each held their own copy of the registry, so a
commit through one silently reverted the other's; and a `Session` derived before a rotation kept
the old Metadata key and would have sealed the registry under it — unopenable by every slot,
permanently. The registry is now one object per `Keystore`, and every handle is bound to the VMK
generation it was derived at: after a rotation, anything older refuses with `ErrStale`. Also
fixed: a commit failing at or after the superblock write now poisons the handle
(`ErrIndeterminate`, `Keystore.Broken`) instead of leaving memory and disk disagreeing; the
post-commit trim keeps the losing copy's registry addressable, so losing the live superblock
opens the file one commit behind rather than not at all; `registry_off` had no upper bound and
could wrap `ValidateExtents`; the new VMK was not zeroed on `Rotate`'s error paths; `create`
left a file behind on one failure path; `Create` returned an `Unlocked` with no way to reach the
`Keystore`; `recipient_id` is now unique within a region (R21) and `AddSlot` checks it; a
`HardwareSlot` with Argon2 parameters and no password is refused; the fuzz target caps Argon2
memory; and two tests that could not fail now can.

**Not built, on purpose.** No physical-memory checks for Argon2 (DESIGN §6): they belong to the
layer that owns the UI's parameter choice, with an OS query this package should not carry. No
retired-slot semantics beyond ignoring them: nothing in v1 creates one. No automatic clearing of
a spurious `rewrap_stale` bit.

---

## 2026-09-05 — internal/archive: designed under critique first, then built

**Process change.** The archive layer has more design decisions than any layer before it —
allocation with an unknown compressed size, in-place edits, crash atomicity across three
relocatable structures, readers that outlive the writes under them — so the design was written
down and critiqued before a line of code: three independent Opus critiques (crash atomicity and
allocation / the API the app and preview server need / spec conformance and the real APIs of the
packages below), 74 findings, with seven questions the draft asked answered by all three. The
critique reversed or sharpened most of the draft. What follows is the design as built.

**The allocation pool is not the published free map** (`FORMAT.md` R31, `DESIGN.md` trap 20).
The draft freed extents into the map at the commit that released them and let the next
transaction allocate them. All three critics pointed out that the next transaction would then
overwrite exactly what the losing superblock copy still references — its index, its free map,
the data of files just deleted — so the A/B pair would protect against a torn write of the
current commit and nothing else. The keystore had made the same mistake in miniature (the trim
that destroyed the loser's registry). Now: what a commit frees is published as free but
quarantined from allocation for one further commit; reader-held extents are quarantined for the
reader's life; nothing is truncated but a reservation the transaction made itself at the end of
the file. A torn live copy opens one commit behind with every file of that state intact, and the
test proves it by committing on top and then tearing the live copy.

**The free map is appended, and the index goes wherever it fits.** The draft wrote the map into
free space, which is a fixpoint problem — the map must describe the extent it occupies, whose
size depends on the map. Appending it at the end of the file dissolves the problem; the map is
at most 64 MiB and its old extent is reclaimed two commits later.

**Small compressed files are sealed into memory first; large ones reserve at the end.** The draft
reserved `MaxEncodedSize` — a bound slightly *above* the plaintext — out of a first-fit hole for
every compressed file, which the critics showed would fragment the map into tails nothing could
reuse and make an edited file grow the archive without bound. Files up to `InMemoryBelow`
(8 MiB) are now compressed and sealed into memory and placed at their exact size, exact fits
preferred; larger ones reserve the bound at EOF and truncate the unused tail; raw files, whose
stored size is exact, go first-fit.

**Registry first on key rotation** (R33, trap 21). The draft left the order open. It is not
open: an archive re-sealed under a key that exists only in RAM is lost on the next crash; a
registry that holds a key the archive has not adopted yet is harmless, because Open takes every
kid the registry knows and reports which one opened the index. Open never writes; a stale
envelope after an interrupted rotation is a reported condition and `RepairEnvelope` is explicit.

**Also from the critique:** `Create` (there was none), an explicit transaction so that adding a
folder is one index rewrite rather than one per file, `ErrIndeterminate`/`Broken` after the
commit point, a single writer per path (an in-process table plus a Windows byte-range lock at
offset 2^62, where it cannot block reads of real data — the first draft locked byte 0 and could
not read its own envelope), `Compact` as a method that carries tombstones and the dictionary,
keeps `archive_id`, verifies the new file and closes the handle before the rename, an
independent `Reader` per request with a Seek on compressed files that restarts and discards (so
`http.ServeContent` works without a plaintext temp file, trap 6), the writer-side overlap
assertion before every data write (§13), `context.Context` on every long operation, receipts
from every commit for the registry's size and time fields, a `NoCompression` option and the
padding knob for trap 8, a tombstone shape (R32) that drops the dictionary reference and keeps
`dek_epoch` monotone, and a probe policy that uses the dictionary without probing for files
below `DictBelow` — the probe has no dictionary and would call a 90-byte JSON record
incompressible for the frame overhead alone.

**Review.** Three Opus reviewers (spec and design conformance / hostile input and crashes /
atomicity and concurrency), 38 findings, 28 verified: 28 confirmed, 0 refuted, 9 nits judged by
hand. Two blockers were found by all three lenses. First, the transaction path ran without the
Archive's mutex: `Tx.Add` iterated the held-extent map while `OpenReader` wrote it, so the
documented case — a preview server holding a Reader per request while the archive is written —
was a fatal "concurrent map iteration and map write". The lock is now taken around every piece
of bookkeeping (allocation, the overlap assertion, the free/held/pool sets) and never around a
compress-and-seal, so readers stay live through a large add. Second, the dictionary: `plan` and
the cached encoder read the committed index's dictionary rather than the transaction's, so a
transaction that replaced the dictionary and then added a small file wrote a frame naming the
old dictionary's ID under an index carrying the new one — a file nobody could ever open — and a
transaction that cleared it and added a file always failed at Commit. Both now read the working
index, the encoder is keyed on the dictionary it was built with, and Commit drops it when the
dictionary changes. Hostile-input blockers in Open: the quarantine set was built with the
unguarded insert that panics on overlap, so a crafted losing superblock crashed Open, and the
live/loser gap merge was quadratic, so a checksummed free map of interleaved one-byte extents
cost hours of CPU on a 72 MiB file. Loser extents now go through a tolerant union after the
losing index is checked against the file, and the merge is one linear pass; deterministic tests
feed a lying free map (over a live file, past the end, tens of thousands of interleaved extents)
and an overlapping loser extent, because `FuzzOpen` cannot reach either through the checksums.
Also a blocker: on any error `seal` left the shared compressor's stream active, and a parallel
encoder flushes what it had dispatched on the next `Reset` — over whatever extent it was then
pointed at; the stream is now abandoned on every failure while the destination is still the
extent the transaction is giving back. The rest: `Compact` released the lock and the path claim
before the rename (now the OS lock goes before the rename and the in-process claim after, so no
second writer opens the original in the window) and leaked the encoders; `Hash` accepted a short
read and hashed stale buffer bytes; `Archive.Close` did not end open Readers (it does, with
`ErrClosed`); `ExtractTo`'s existence check was a TOCTOU because the final rename replaced
(exclusive placement now, `MoveFileEx` without replace on Windows, link-and-unlink elsewhere);
`Open` sorted the caller's key slice; `ID`/`KID` raced `RotateKey`; an unchanged transaction
committed without truncating a tail a failed store had appended (a failed store now gives its
reservation back at once); and the test that was to prove the quarantine could not fail, and the
crash test's fallback case did not depend on it — both rewritten, and a test now aborts an
in-flight transaction over a freed extent and proves the fallback state still extracts. The
race detector needs cgo; the dev machine had no C compiler at commit time, so the concurrency
test was first run repeatedly without it. A MinGW-w64 GCC (WinLibs 16.1.0, UCRT) was installed
the same day and the module's `-short` suite passes under `-race`, the concurrency test twenty
times over; `scripts/test.ps1 -Race` is the wrapper. Release builds keep `CGO_ENABLED=0`
explicit — a compiler on PATH must never turn cgo on by accident.

**Not built, on purpose.** No idle timeout inside the archive (the app owns timers and closes
the handle; DESIGN §10's per-archive timeout is enforced there). No streaming Add of unknown
size: the caller spools, on an encrypted volume, and says so to the user (trap 6). No dictionary
retraining that rewrites existing files. No cross-process advisory beyond the lock: a folder a
sync client rewrites is out of scope for in-place transactions.

---

## 2026-09-05 — `internal/piv`: the token layer as built

The hardware-token layer: `keystore.Token` over a YubiKey's PIV application, plus enrollment —
finding the key a slot record names, generating one, reading the PIN-protected management key.
Windows-only (`//go:build windows` on every file, and on every importer), on piv-go v2.6.0 with
a PC/SC layer of the package's own for what piv-go cannot do. The design was critiqued before
any code by three Opus critics (security and threat model / PC/SC, YubiKey and piv-go realities
/ API fit and testability), 37 findings, 34 verified adversarially: 24 confirmed, 10 refuted, 3
nits judged by hand. Every critic independently found the same blocker.

**The reuse predicate is exact, and the same one everywhere** (trap 22). The draft admitted any
touch policy "not never", which let a *cached* key — released for 15 s after one touch — through
the one invariant SCOPE.md calls immovable; and it never looked at the PIN policy, so a
*pin=never* key would have been enrolled with the PIN factor silently gone, and a Bio key
enrolled and then refused at every unlock. `KeyInfo.Usable` is now P-256, public key reported,
touch *always*, PIN *once* or *always*; `Generate` accepts only what `Usable` would accept;
`Token` asserts it again. A key that fails is reported with the reason and left where it is.

**Occupancy is a key or a certificate.** GET METADATA alone read a certificate-only slot —
another program's provisioning — as empty, and the "first empty slot" rule would have generated
over it. `Inspect` reads the certificate object too; only "not found" means absent, and an
unparsable certificate is still an object. `GenerateOptions.Slot` has no "pick for me" zero
value, because the one call that can destroy a key names its target: `FirstEmptySlot` picks,
`Generate` takes the slot, and `Overwrite` is only ever about that slot.

**The package owns a PC/SC probe and the reset, not the transport** (trap 24; answers the
draft's open question). piv-go's `Open` leaks an exclusive connection on two failure paths and
its handle is unexported; forking its 300-line PC/SC layer would in truth mean forking the
library, since nothing above it has a seam. Instead every reader is probed first over the
package's own winscard connection — SELECT PIV, GET VERSION, then a disconnect that resets the
card — with typed errors from the real return codes, so the reachable leak (a YubiKey with PIV
disabled, a non-PIV card behind a "yubi" reader, a busy card, an old firmware) is refused before
piv-go connects; only the microsecond race between connect and transaction remains. The reset on
`Close` connects *shared* — the mode that was measured to work, and the one nothing else can
refuse since it transmits nothing — with the context and reader name prepared before piv-go's
own disconnect, so the gap is sub-millisecond; a failed reset reconnects and asks the card with
the retry-free empty VERIFY, and warns only if it is still verified — or, after a `Generate`,
whenever the reset did not happen, since the management-key authentication piv-go leaves on the
card cannot be asked about. A read-only sweep of the readers (enrollment step 1) still resets
each card the probe succeeds on, once, before piv-go connects — that is how a `Card` starts
unverified whatever a program that has since released the card left behind (a card another
program still holds is refused with `ErrBusy`, not reset) — and closes without a second reset,
because nothing was verified through it.

**The ceremony is the package's, not piv-go's.** The PIN is read from the caller's `Prompter`
with the card's own state shown first — the retries, or that they are unreadable because the
card is already verified (a card answers the empty VERIFY with success then, and the draft would
have shown "0 retries"; a blocked PIN is not prompted for at all) — verified by the package,
and piv-go is told the key needs no PIN so it can neither prompt nor verify. Touch goes up after
the VERIFY and before the agreement, numbered: `Prompter.Touch` carries the operation's ordinal,
and a `Token` performs at most `MaxOperations` (8) before refusing, so a run of prompts is
countable and bounded. Status word `6982` is disambiguated after the fact (trap 23). The ECDH
result is piv-go's own buffer so the keystore's zeroing reaches it; the management key read from
the PRINTED object is cloned for the caller and zeroed in piv-go's decoded response.

**R34, one token, one slot** — found on the keystore side by the security critic. `AddSlot`
refused a duplicate token, but the region is only checksummed and `Unlock` walks every slot:
a spliced file naming one token in 32 slots would have run 32 touch prompts before the R25 check
could report tampering. The decoder now refuses a repeated `slot_pubkey` as it refuses a repeated
`recipient_id`, and the keystore's invariant check refuses to produce one, so `Create` is covered
too. With that, at most one slot can ever match a token and one unlock is one ceremony.

**Also from the critique:** `Attest` returns a *verified* statement (piv-go's `Verify` against
the roots it embeds; the F9 certificate object is the one read outside the allowlist, PIN-free
and retry-free) or `ErrAttestation` — never an unchecked certificate; the allowlist is stated as
what it is (key operations target 9d and 82–95 only; the management-key authentication piv-go
runs inside `Generate` names object 9b, and the fake asserts what crosses the interface, the
hardware tests the rest); `Card` has a lifetime (`ErrClosed` after `Close`, idempotent `Close`,
`ResetFailed` sticky) and a concurrency rule (one operation at a time, a second refused with
`ErrInUse` rather than queued, `Close` waits for the one in flight); the ECDH call takes the
already-validated `*ecdh.PublicKey`; `DefaultManagementKey` is a function returning a copy;
`ParseSlot`/`AllSlots` replace an index-based constructor; the firmware floor is 5.3 (GET
METADATA) and is checked in the probe, before piv-go connects; PINs longer than 8 bytes or
empty are refused before any APDU.

**Not built, on purpose.** No `Inspect` outside the allowlist — the overwrite confirmation names
the slot being overwritten and what it holds, and a token inventory can wait for a UI that wants
one. No Bio support. No `piv_slot`/`token_serial` record fields: the slot is found by public key
from metadata without a PIN, and the token by trying each reader, so the fields remain the UX
optimisation they were proposed as and the decision stays open (`Serial` is exposed for a
registry-side note later). The PIN policy and the session semantics stay the user's call: the
package builds both mechanisms — hold the `Card`, or close and reset — and decides neither.
`tools/pivtool` (readers / info / generate / selftest / resetcheck) is the in-repo way to run the PIN-bearing
steps in the user's own terminal; it never overwrites, never tries the factory management key,
and reads the PIN without echo and never from an argument.

**Measured on the user's YubiKey 5.7.4 (USB-A Keychain) on 2026-09-05**, through the finished
package and `tools/pivtool`, every PIN typed in the user's own terminal: the read-only sweep
(probe, exclusive open, PIN state, metadata of all 21 slots, first empty slot, close without
reset) takes 180 ms; a second `Open` on a held card is refused at the probe with `ErrBusy` and an
unknown reader name with `ErrNoReader`, from the real return codes; generation of P-256 in 9d
with PIN once / touch always, using the PIN-protected AES-256 management key, 635 ms; the
attestation of the new key verifies against Yubico's roots and agrees with the metadata and the
serial; two ECDH rounds on the token matched `crypto/ecdh`, the first in 5.0 s including the PIN
and the touch, the second in 1.6 s with the touch alone — PIN once asked once, touch asked every
time, the prompt numbered. `pivtool resetcheck` proves the trap-14 mechanism end to end: a
VERIFY through a Card, `Close`, and the card answers "not verified" to a shared-connection probe
from outside, well inside the 10 s window that would otherwise keep it verified.

**Review.** Three Opus reviewers (conformance to the design, the critique and the docs / PC/SC,
piv-go and YubiKey realities / Go correctness, concurrency, secrets and tests), 18 findings, 15
verified: 11 confirmed, 4 refuted, 3 nits judged by hand. The blocker was found by all three
lenses: `Close` sampled its "something to reset" flag *before* waiting for the operation in
flight, so a Close issued while the PIN dialog stood open — the UI's cancel path — waited for the
PIN to be verified and then released the card without a reset, returned nil, and reported
nothing: the one state the mechanism exists to prevent, behind a successful Close. The flag is
now read after the wait, and a deterministic test holds an operation in its prompt, closes from
another goroutine, waits for the Card to mark itself closed, releases the prompt and checks the
reset happened. A major on the same path: the fallback probe after a failed reset can only ask
about the PIN, while `Generate` leaves the management-key authentication on the card — which no
APDU can ask about — so a failed reset after a Generate now reports `ErrResetFailed` regardless
of what the PIN probe says, and the reset's two halves are split so that the PC/SC context and
the reader name are prepared before piv-go's disconnect, as the entry above already claimed.
Also fixed: a verified state a Card did not create (a probe reset that did not take, another
program's VERIFY) is no longer trusted for a PIN-once key — the PIN is asked once; `Generate`
classified a card pulled during the management-key handshake as "management key refused", the
one message that points a user toward a PIV reset (transport first now); the keystore's R34 check
walked active records while the decoder walks every non-empty one (both non-empty now, in the
invariant and in `AddSlot`); `pivtool` read the typed management key through a string it could
not wipe (bytes end to end now, the PIN's raw bytes wiped too); the shared secret was dropped
unwiped on the one error path that does not return it; and two sentences of this entry said the
opposite of the code about the probe's reset and the read-only sweep. Refuted, with reasons
worth keeping: piv-go's `Metadata` already maps a missing PRINTED object to an empty struct, so
`ErrNoProtectedKey` is reachable; the probe cannot close piv-go's leak on the connect-to-Begin
race and the docs say so. The three lenses agree on what holds: every string the package matches
is verbatim in piv-go v2.6.0; `KeyAuth{PINPolicyNever}` suppresses piv-go's VERIFY and its
metadata fetch inside the one transaction that spans our VERIFY and the agreement; the 258-byte
receive buffer with 61xx chaining, the SCARD_IO_REQUEST, the multi-string parse and the SELECT
APDU are right; no path burns a retry without the user's intent.

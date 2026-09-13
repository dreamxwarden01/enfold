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

---

## 2026-09-05 — Decided: PIN + touch at every unlock, the token released at once

The user's ruling on the token policy `SCOPE.md` had left open: **the first unlock is PIN and
touch, always.** Together with DESIGN §10 that settles the rest, because after the unlock the
token has nothing to do: the VMK is derived once and destroyed, the KWK, DB and Metadata keys
are what a session uses, archive keys come out of the registry under the KWK, and the token is
next needed only at the next unlock — after the idle timeout, the absolute cap, a workstation
lock — or for a slot mutation, which is its own ceremony. So there is no session to hold the
card for: the `piv.Card` is opened for the unlock, `ECDH` runs the ceremony (PIN with the
retries shown, then touch), the VMK is derived, and the Card is closed — which resets the card,
so the PIN-once state does not survive it (trap 14) and every other CCID application has the
token back within a millisecond. The 9d key stays PIN policy *once* (DESIGN §3): *always* would
change nothing the user sees, since one operation follows one VERIFY either way, and *once*
keeps the design's option of a second operation in the same ceremony without a second PIN.
"How long a verification stands" is therefore: as long as the Card, which is as long as the
unlock. The `SCOPE.md` bullet is closed; the invariant in it stands.

**Restated the same day:** closing to the tray destroys the window; it switches to hide only if
the real UI shows delay, stutter or state loss on reopening (the 2026-09-04 ruling, unchanged).

---

## 2026-09-05 — Decided: the keystore is dated, the export rule stands, no token fields, one YubiKey

The user's rulings on the questions the token layer left, plus one addition.

**The keystore export keeps its rule** (2026-09-01, R28: registry plus the recovery slots,
nothing else) — the question had been misread as one about the keystore, and the reasoning
stands unchanged. **But a keystore must carry its own last-modified time**, independent of
filesystem metadata, so that two copies can be told apart: R35, `modified_at` in the
superblock, written on every commit, never decreasing, an export dated by its own creation, and
bound into the registry's AAD so that a doctored date fails to open. It is plaintext on
purpose — the question "which backup is newer" is asked before the recovery key comes out —
and the registry's own timestamp, already there, now carries the same value.

**The recovery record is on by default for volume exports, at 3%**, changeable in settings
(the RAR default; `SCOPE.md` 1.1).

**No `piv_slot` field.** The slot is scanned: 9d first, then the retired slots 82–95, by public
key from metadata with no PIN and no touch, and a token that holds none of the keystore's keys
is reported — which is what `internal/piv` already does (`Find`, `AllSlots`). Nothing is
hard-wired, so a key moved with a newer firmware's MOVE KEY is still found.

**No `token_serial` field, and one YubiKey at a time.** Before the token is inserted the product
cannot know which of several enrolled tokens is coming, so a serial would only help choose
among readers — and Windows and the FIDO stack already refuse to operate with two YubiKeys
inserted, so the UI does the same: with more than one, ask for all but one to be removed. The
lock screen shows the slot's `label`; that was judged enough. The 2026-09-04 proposal is closed,
its registry-side parenthetical included: no serial anywhere in the keystore, registry included;
`piv.Card.Serial` stays a runtime value for the UI.

**Review.** Two Opus reviewers (the design's claims / Go and tests), 8 findings, 4 verified: 2
confirmed, 2 refuted, 4 nits judged by hand. Both lenses found the same latent trap: `commit`
advanced `modified_at` on every transaction but re-sealed the registry only when one was passed,
so a registry-less commit — a shape no caller uses but the code accepted — would have flipped
the superblock onto a ciphertext that no longer authenticates, and no credential would have
opened the file again. Every commit now requires a registry, refused before anything is
written, and a test drives both registry-less shapes. Refuted, with the rule restated: an export
is dated by its own creation and does not inherit the source's monotonic floor (R28, R35 — a
clock set back can date a newer export older, and the registry's own timestamp is there for
the tie); a negative date is refused at decode as any other malformed field. The nits were
wording: "volume exports" in SCOPE, the R35 sentence that claimed a doctored copy cannot lie
when it can only fail to unlock, and this paragraph's own predecessor about the serial.

---

## 2026-09-06 — Decided: the Native look for 1.0; a second look deferred; the UI is a client of the core

**How the direction was chosen.** With no visual direction in hand, four independent mockups of
the same four screens with the same content were built from one brief (`docs/ui/brief.md`):
Native (a first-party Windows 11 application), Instrument (a dark security instrument),
Ledger (a typographic keeper's book) and Workbench (the dense manager in 7-Zip's lineage). The
user liked Native and Workbench and asked whether both could ship as two selectable looks of one
interface. A prototype answered that (`docs/ui/two-looks.html`): one DOM, two token sets, and
seven structural CSS rules — feasible, and cheap to keep. The user then chose to ship **Native
alone in 1.0** as the modern face of the product, and to consider Workbench later as a second
look, on the condition that adding a look does not touch the business logic.

**Why the condition holds, and is now a rule** (DESIGN §14). The core is Go and owns every
decision — sessions and timers, the ceremony's steps, staged changes and their commit, the slot
invariant — behind one service API that Wails binds into the WebView; the frontend renders and
reports. `piv.Prompter` already shows the shape: the UI implements an interface the core calls.
So a second look is a second frontend or a second skin, and the core does not know which is
running.

**Also settled by the mockups.** The ceremony is one panel changing in place — waiting, PIN,
touch — with a small three-step line above it so the user knows a touch follows the PIN; the
mockups' three side-by-side panels are three moments of that one panel. The touch moment is a
full-surface takeover. Settings carry Appearance (Look, Theme). `docs/ui/native.html` is the
reference the app is measured against; the three rejected directions are not kept.

---

## 2026-09-06 — The application layer designed and critiqued; what the critique changed

`APP.md` is the result: the Go core (`internal/app`), the Wails v3 shell and the Svelte 5 +
TypeScript frontend (the user's choice, 2026-09-06). The draft was critiqued before any code by
three Opus critics — security and the session model / Wails v3 beta.16, WebView2 and Win32
realities / architecture, API fit with the built layers and testability — 55 findings, 54
verified adversarially: 48 confirmed, 6 refuted, 1 nit. The confirmed ones rewrote the design in
these places.

**The preview transport outlives the session.** The draft stopped the loopback server at lock,
which killed the in-flight stream DESIGN §10 promises survives a lock, and its per-session
token would have broken every seek. The server now binds once per process (the CSP names its
port), the token is per open archive, and it dies with the last open archive; a live reader is
archive activity, never session activity. The Wails asset server was measured and rejected for
this: on Windows it buffers the entire response before handing it to WebView2 — an 800 MB
preview would be 800 MB of plaintext in the Go heap.

**What a lock does, precisely.** The draft's "registry copy dropped" had no mechanism: the
decrypted registry lives on the keystore handle, so the lock path is now cancel the mutation,
`Session.Lock()`, `Keystore.Close()`, with the lock screen's four plaintext facts cached. A lock
trigger is a state-machine input accepted in every state — in the draft a workstation lock
during a ceremony did nothing, and the vault would have unlocked behind a locked screen; now the
ceremony is cancelled and latched at its publish point. Zeroing is synchronous on the message
thread because Modern Standby freezes desktop processes about two seconds after the display-off
notification, and `PBT_APMSUSPEND` — the draft's only sleep trigger — never fires on those
machines; display-off is the primary trigger now. `WTSRegisterSessionNotification` can fail and
was unchecked; it is checked, retried, substituted by a poll, and surfaced as a warning.

**Save after a lock.** `Tx.Commit` does not consult the keystore, so a Save on an archive that
outlived a lock would have advanced the file and lost the registry receipt. Save and Compact
are gated on `Session.Live()` (a new keystore method), one mutex spans commit → receipt, and a
receipt a hardware trigger still manages to strand is owed in memory and applied at the next
unlock. Every Save also re-hashed the whole file for `last_ciphertext_hash` — 48 GB of reading
for three renames; the registry gains `last_seq` as the keyless identity and `hash_at_seq`, and
the hash is refreshed only by Compact and an explicit Verify.

**Rotation and the Session.** `Unlocked.Rotate` advances the generation and makes every existing
Session refuse with `ErrStale`; the draft never re-derived it, so removing a slot would have left
the vault dead behind a running countdown. The core now derives the next Session from the
rotating `Unlocked` and locks the old one in one critical section. And "registry after the
archive's commit, never before" was stated as a blanket rule, which inverts trap 21 for key
rotation: two rules now, content after, keys before.

**Forget was data loss.** The registry record holds the only wrapped copy of an archive's keys.
"Forget" is gone from 1.0; `Hide` sets a policy bit; the destructive form, if ever wanted, is a
`ForgetKey` that names what it destroys. `RestoreArchiveRecord` from a backup exists instead.

**Two processes.** No single-instance guard and no keystore lock meant two Enfolds would have
clobbered each other's superblock commits: Wails' single-instance guard in the shell (per logon
session), an exclusive OS lock on the keystore (`keystore.ErrBusy`), and an optimistic `seq`
check at the keystore's commit point.

**Secrets and the bridge.** Wails reflects over every exported method of a registered service,
promoted methods included — an embedded `Session` would have put `DBKey()` on the WebView's API.
Bound service types are now structs with unexported fields in their own package, guarded by a
test over their reflected method sets and by committed bindings. Every bound argument is
marshalled and stringified before any log-level check, so the PIN, the passwords, the recovery
digits and the management key travel on the raw message channel; the frontend's "cleared on
submit" claim is replaced by the honest one. Typed errors would have reached the frontend as
`{}`: one `app.Error{Code}` whose `Error()` is the code alone, one `classify`, one
`MarshalError`. WebView2's defaults were wrong for a renderer that is the untrusted side:
context menu on (right-click "Save image as" writes plaintext to Downloads), every permission
granted when no map is set, the Edge PDF viewer's toolbar with Save and Print that cannot be
hidden — context menu off, every permission denied, no PDF viewer in 1.0 (Extract only), a CSP
and a Permissions-Policy header from the asset middleware. And the draft's "the profile runs
with the cache disabled where Wails exposes the option" resolved to nowhere: `no-store` is the
whole defence and trap 13 now says so; inspecting the profile directory is a release gate.

**The rest, briefly.** Settings move to `%LOCALAPPDATA%` and the timeouts into the authenticated
registry with clamps and no way to express "off"; the activity heartbeat is a request the core
grants only when `GetLastInputInfo` agrees; the entangled password is asked before the PIN, not
after the touch, because the credential is assembled before any prompt; prompts have identities
so a double-click cannot spend two retries; `Compact`, `RotateKey`, extraction, adding, folder
projection, renames and drops each got the preconditions and collision rules the built layers
actually impose; the tray has three states; a dirty archive is never silently closed and never
unbounded; the shutdown hook commits dirty archives inside Windows' budget instead of asking;
events are subscribed before the first fetch and carry sequence numbers; the frontend's
dependencies are pinned with a lockfile. Six findings were refuted with reasons recorded in the
run: the session-token blast radius (file ids are random), the "more than a page crosses"
reading of §1, `Options.MarshalError` as the error fix, `x/sys/windows` being unable to build a
message window, wall-clock deadlines (Go's timers already include suspend on Windows), and a
double `PBT_APMSUSPEND`.

**Rulings taken here, for the record.** The VMK lives only for a mutation and slot changes re-run
the ceremony (a grace window would defeat the reason §10 destroys it). The registry gains
`last_seq`, `hash_at_seq`, `idle_minutes`, `absolute_minutes` and two policy bits (`hidden`,
`no_compression`); it does not gain file counts. Svelte 5 + TypeScript. **Open for the user:**
an unelevated BitLocker check — the documented mechanism is admin-only, so either a measured
alternative exists or the SCOPE bullet moves.

## 2026-09-06 — The application layer, built (checkpoint; the review is still running)

**What landed.** `internal/app` (the core of `APP.md`: session state machine, ceremony with
prompt mailboxes, archives with the staged overlay and two clocks, operations, the loopback
preview server, settings, receipts owed and paid), `internal/app/api` (the bound view structs,
guarded by a reflected-method allowlist test and `MarshalError`), `internal/app/pivcards` (the
one importer of `internal/piv`), `internal/brand` (the mark and the tray icons drawn in code),
the Wails shell at the repository root (`main_windows.go`, `tray_windows.go`,
`lockwatch_windows.go`: single instance, three-state tray, the window created on demand, secrets
over the raw message channel, CSP and Permissions-Policy from the asset middleware, the
message-only Win32 window for WTS and power notifications, `GetLastInputInfo` as the activity
corroborator, the ordered shutdown), the Svelte 5 + TypeScript frontend on the Native look with
its generated bindings committed, and the `wails3` build pipeline trimmed to Windows.

**Verified.** The core runs against a fake `Cards`/`Card`, a manual clock and an event
recorder: password and token unlocks, wrong PIN and retries, parked states (busy, no match,
blocked), the prompt deadline, lock triggers during a ceremony, idle and absolute timers, the
heartbeat needing real input, the archive round trip through the projection, the archive
clocks, shutdown committing dirty archives, settings in the registry, enrolment and removal,
vault creation with the one-time recovery URL. The production build was started against a
throwaway data folder (`ENFOLD_DATA_DIR`): the window, the tray and the lock watch come up, the
page renders under the production CSP. Every screen was exercised against a mock backend that
answers the Wails call transport.

**The review, so far.** A five-dimension review (core state, archives and operations, shell and
Win32, frontend, the stage-1 format and keystore changes) with adversarial verification is in
progress. The core-state dimension's findings were real and are fixed in this checkpoint: a lock
trigger in Unlocked left a running slot-change ceremony — the VMK and the PIN-verified card —
alive until its prompt deadline (`LockNow` now latches and cancels the ceremony in every state,
and the lock's close of the handle waits for it); a rotation could install the re-derived
session behind a lock that landed meanwhile (it re-checks the state and zeroes the new keys
otherwise, and a session is never replaced without being zeroed); slot changes committed to
the keystore from their own goroutine while registry receipts were written under the state
mutex (**registry writes now wait for a slot change** — a Save meanwhile owes its receipt, paid
when the ceremony ends — and a slot change does not start while a registry-writing operation
runs); `Save` held the state mutex across the archive commit, so a Modern Standby lock could
not zero the keys in time (the commit runs under the archive's own mutex only); enrolling a
second YubiKey opened the reader while the unlocking key was still held exclusively and parked
at "busy" (the key is released first and the reader set must empty before the next key is
accepted); a cancel during derivation was ignored at the publish point (the cancellation is
checked there, as the latch is). Also fixed: a stale session is locked rather than reported as
"internal", `CreateVault` keeps the configured vault until the new one exists and is refused
in Broken or Busy, an open of another vault closes a Broken handle and forgets its owed
receipts, a reopen waits for a pending lock's close instead of reporting the vault busy, a
parked unlock releases the vault file, a missing or garbage vault file is named as such, the
absolute deadline follows a changed setting and the idle deadline is never extended by a
non-input path, timeouts can be restored to their defaults, an indeterminate or conflicting
slot-change commit enters Broken, only lock contention means "open in another Enfold", and an
export is dated no earlier than the vault it came from. The remaining dimensions' findings are
handled in the next entry.

## 2026-09-06 — The application layer's review, second half

The remaining four dimensions of the review (archives and operations, the shell and its Win32
code, the frontend, the stage-1 format and keystore changes) were triaged from the journal of
the stopped run; every finding a verifier confirmed is fixed here, with the tests that pin it.

**Archives and operations.** A failed or cancelled `Compact` left the archive handle and its
file lock alive (the handle is now closed whatever `archive.Compact` returned, and the clean gate
is re-checked once the operation holds the archive's mutex, so a change staged in between keeps
the archive rather than losing its transaction); closing an archive whose save ended
indeterminate dropped the entry without closing the handle, so a reopen reported it busy; the
figures shown under the state mutex were read from the handle, whose own mutex a running hash
holds for the whole file — `size`, `files` and `free` are cached at every snapshot instead, and
`ListArchives` asks the file system with the mutex released, so a lock trigger never waits on
I/O; replacing a staged add made a second overlay entry (the row could no longer be un-staged),
now one object; a cap or idle callback that was already running when its clock was re-armed or
cleared acted anyway (each clock carries a generation the callback must still match); a file
whose seq disagrees with the registry was logged and adopted, now `archive.copy_mismatch` on the
stat and the list; `RotateKey` adopted the new kid before the archive was rewritten; the
extraction containment check rejected names beginning with two dots; a negative page offset
panicked; preview responses carry `Content-Security-Policy: sandbox`.

**Frontend.** The recovery key of a newly created vault was never shown: the reveal lived in
the lock screen, which unmounts on the Unlocked event that precedes the ceremony's final event.
The reveal is rendered by the root above every route, for any ceremony. Starting a ceremony
while one ran dismissed the visible panel and then failed (`ceremony.in_progress`): a live
ceremony is cancelled and its end awaited first. An unlock made from an open archive left the
locked banner in place (the archive's stat now follows the session). Folder rows carried an
empty file id, so selecting one selected every folder: rows are keyed by kind and path, and
extraction and deletion act on files only. A boot snapshot could overwrite a newer state event
(the sequence rule now admits an equal-seq snapshot and nothing older). The expiring prompt
showed only on the archive's own page, so a dirty archive left elsewhere ran to the cap unseen:
it is asked at the root. The tables were mouse-only; rows take focus, Space selects, Enter
opens, the arrows move.

**Shell.** The poll fallback for workstation locks read `SessionFlags` at the wrong offset of
`WTSINFOEXW` (the union is 8-byte aligned: offset 16, not 12), so it would have locked every
five seconds; a mirror struct fixes the offset and the unknown state counts as no answer. The
quit confirmation could never return: a Windows question dialog is a Yes/No message box and
Wails runs only the callback whose label matches — the buttons are "Yes" and "No" and `Show` is
awaited. Cancelling a native file dialog reached the page as "internal" (the common-file-dialog
wrapper reports cancel as an error): it is "nothing chosen". The tray's click ran on the Wails
main thread while other callers hold the window mutex across main-thread calls, a deadlock: tray
and menu callbacks leave the thread first. The poll fallback exited at once when the watch
window could not be created (it has its own stop signal). A refused secret is reported back as
`secret.refused`; the WebView2 profile is removed at exit, best effort; the MSIX task pointed at a
file that does not exist. Branches for broadcasts a message-only window never receives were
removed and `APP.md` §5 says why.

**Keystore.** The OS-level lock was never exercised by a test — the in-process table refused the
second handle first; a `\\?\`-spelled path bypasses the table so only `LockFileEx` can refuse.

**Refuted, with reasons in the journal.** The reopen-after-lock race (the state event is emitted
after the close) and parked ceremonies holding the file (neither park is reachable there) — both
guarded anyway, since the guards are cheap and the design reads better with them.

**Verified again.** A second workflow (three sweeps over the changed code, every finding above
low verified adversarially; 15 agents) confirmed twelve more, all fixed: a file that fails to
open replaced the configured vault and demoted the state to "none" (the vault's path and facts
now stand unless the open succeeds or the file is busy, and a garbage file is `vault.invalid`);
a failed create from a fresh core landed in Locked over an empty path (a ceremony records the
state it returns to); a rotation's commit opened a window in which any caller saw a stale
session and locked the vault (a stale session during a slot-change ceremony is
`ceremony.in_progress`, not a lock); shutdown during a slot change stranded the receipts it
committed (it waits for the cancelled ceremony first); token enrolment parked with the key to
enrol still held; a Save whose commit failed before the commit point kept showing changes the
archive had already discarded; closing an archive during its compaction blocked for the whole
run; the copy-mismatch flag was never cleared; a compaction whose reopen failed lost its receipt
(recorded before the reopen, at seq 1); an indeterminate key rotation left the archive "open"
on a broken handle; a recovery-key prompt could be dismissed as if it were the reveal; a page
reply could land for the wrong folder. Also from the sweep: an add in which every file is
skipped no longer leaves the archive "dirty with nothing to save"; a dismissed reveal is not
brought back by the ceremony's final event; rows take a roving tab stop; a drop with no target
is refused; secrets are accepted from the main frame only. Left as documented limits: file I/O
of an abort or close under the state mutex, and an extract that overlaps a compaction losing
the rest of its batch.

---

## 2026-09-06 — Volume names, the password before the key, motion

Three rulings from the user after the first look at the built application.

**Volume export names follow RAR, not `.001`.** `<name>.part01.efd`, `part02`, …; a two-digit
ordinal, widened to the digit count of the part total when an export has more than 99 parts
(`part001` … `part120`). The count is known before the first part is written — the export splits
a finished file — so the width never has to change mid-export, and the parts sort in order in a
plain lexical listing, not only in Explorer's natural sort. A bare numeric extension (`.efd.001`)
is what Windows associates with nothing; keeping `.efd` on every part keeps the parts Enfold's.
The user asked for "increment past 99" (`part100`, `part101`); the padded-to-count form gives
exactly that for a large export and additionally keeps the order in tools that sort by string.
`SCOPE.md` carries it; nothing in code yet (volumes are 1.1).

**A chosen secret comes before the key.** Create and enrol for a token with an entangled
password used to run the token flow first — wait for the key, PIN, management key, `Generate` —
and ask for the password after. A cancel at the password step had already generated a key on
the token for nothing; and the user rightly expects to type the password they are adding
*before* the hardware is initialised. Both flows now ask for the password first. The ceremony
state gained `Choose`, set on prompts for a secret the user is choosing now (a new standalone
password, a new entangled password) and never on a prompt for one they hold, so the page labels
the field "choose a password" and the enrol panel — which can ask for a current password and
then a new one — never shows the same words for both. The dialogs stop asking for a "Label"
under a password way in: a token is named ("Name this key"), a password slot is "Password",
and the dialog says the password itself is chosen in the next step — a typed secret never
rides a bound-method argument, so it cannot be a dialog field. The dialogs also send
"Password" as a password slot's name whatever was typed for a key before the kind was switched.
Tests: enrolment and creation with an entangled password both see the password prompt before
any card is opened for the new key, and the new key then unlocks with password + PIN.

The review of this change found what the reorder had moved: the key that unlocked was now held
open across the password prompt — up to the prompt's hour — and a user who pulled it there (as
the dialog's own note invites) would get a "may stay PIN-verified" warning that never clears.
The unlocking key is released before the prompt, and the enrol test asserts it. The same class
— a verified (dirty) card held across a prompt, whose close reports a reset that never happened
if the key is pulled — exists on older prompts where the card is held on purpose: the PIN
re-prompt after a wrong PIN, the entangled-password retry, and the enrolment's PIN and
management-key prompts; the warning that never clears is a follow-up. Releasing early opened
the next hole, found by the verification of the fix: the swap could now happen *during* the
prompt, and a wait for an empty reader set would then stand for the prompt's hour — a swap in
the same port shows the same reader name throughout. The wait is now for the unlocking key to
be out of the reader, probed by content (the card present is opened for its public keys, no
PIN, no touch, once per poll), and the swap step names the key to remove while it is still in.
Tested: the swap during the prompt, and the step's wording while the unlocking key stays.

**Motion.** Every dialog, menu and popover fades in over 140 ms (dialogs also scale from 97%)
and out over 100 ms; a panel that swaps content in place fades the new content in; the lock
screen's cards ease between live and dim; toasts fade. `prefers-reduced-motion` zeroes the
durations — checked when a transition starts, through Svelte's `prefersReducedMotion`, so a
preference changed while the app runs is honoured; the stylesheet's token collapses under the
same media query. Svelte's transitions are used with `|global` on dialogs so the fade plays
however the dialog came to exist; a card body that fades in must sit directly in its `{#key}`
block, since a local intro inside a freshly created `{#if}` never plays. Because a fading-out
dialog stays mounted until its fade ends, it ignores Escape and backdrop clicks from the moment
its outro starts — otherwise a second Escape could dismiss the ceremony behind it, and with it
the one-time recovery-key reveal. There are no menus in the frontend yet; the rule is written so
the first one inherits it.

**One vault per user** was raised here and ruled the same day; the next entry has it.

---

## 2026-09-06 — One vault per user, at a fixed place; importing a vault or a backup; NSIS only

**Ruled by the user:** one keystore per Windows user, at `%LOCALAPPDATA%\Enfold\vault.eks`; a
vault or a backup from elsewhere is imported through the app; copying the file into the folder
by hand is acceptable but not what the app recommends; a backup is told apart from a full vault,
and a backup leads into the setup flow after the recovery key; the installer is NSIS only.

**Why one vault.** Beyond the management confusion the user named — several vaults sharing one
YubiKey — there is a hard reason: the registry is the one index of which archive opens with
which key, so with two vaults on one token the user opens vault A and is told an archive has no
key, and that error cannot be explained. `DECISIONS.md` 2026-09-01 already said "one per device,
default `%LOCALAPPDATA%`"; the create dialog's free file picker had drifted from it.

**Why `%LOCALAPPDATA%`.** Per user, machine-local (no roaming), on the system volume the
BitLocker warning covers, in the folder that already holds the settings, the log and the
WebView2 profile. ProgramData is shared by every user of the machine; a per-user secret does not
belong there. The install folder is read-only for a standard user under Program Files, and it
would tie the vault's place to the executable's.

**Installed or portable: decoupled.** Where the executable lives and where the vault lives are
two questions. The executable is one file that runs from anywhere (WebView2 ships with Windows
10/11); the NSIS installer is a convenience. The vault never sits beside the executable: Program
Files is not writable; a "green" copy on a USB stick would carry the keystore off the
BitLocker-protected volume, which is exactly what FORMAT §16 warns against; and two copies of
the executable would see two vaults, which is the confusion being removed. **MSIX is dropped**:
a packaged app's AppData writes are virtualised to `%LOCALAPPDATA%\Packages\<id>\LocalCache\`,
so the MSIX-installed app and a plain executable would see two different vaults.

**Importing.** `Vault.InspectFile` reads a file's plaintext facts and classifies it: a *backup*
has only recovery slots active (R28's export), a *vault* has at least one other slot.
`Vault.ImportFile` copies the file into place — read raw, never opened as the vault, temp-then-
rename — and opens it. With a vault already there, a copy of the same vault replaces it when it
is newer, or when the current one is Tampered or Broken (R25's one action); anything else is
refused with `vault.exists` unless the page passes `replace` after a confirmation that names what
is replaced. Nothing is destroyed: the replaced file is renamed `vault-<unix>.replaced.eks`
beside the new one and is the user's to delete. The same retirement happens when a vault is
created over an existing one. "Use it where it is" remains as the advanced choice for a vault
kept elsewhere (the `settings.vaultPath` override), and an empty override with a `vault.eks`
already present at start adopts the file — the by-hand copy the user allowed.

**A backup is adopted, not restored.** FORMAT §15 and R28 already say how a backup comes back:
open it, unlock with the recovery key, enrol new slots. That is the setup ceremony (`kind:
setup`): the recovery key first, then the first way in exactly as at creation. Until it
completes the file is a vault with only a recovery slot, which the app never leaves silent:
`VaultStatus.SetupNeeded` is set, `BeginUnlock` is refused with `vault.setup_needed`, and the
lock screen's one action is *Finish setting up*, which reruns the ceremony. A backup opens with
nothing but the recovery key, so the first-run screen says to have it ready. The recovery key
stays valid afterwards; no new one is minted, since the recovery slot is the one the backup
carried. Whether the VMK should be rotated after adoption — the backup sat wherever the user
kept it, protected by the 128-bit recovery key alone — is left as it is: the recovery key is
the design's stated protection for a backup (FORMAT §15), and a rotation is one click away.

**Not done with this:** restoring one archive record from a backup or another vault
(`RestoreArchiveRecord`) stays deferred (APP.md §12); the vault file's own name is fixed, the
display name is the machine-local setting.

**Critiqued before it was built (2026-09-07), and changed.** Three critics over the design as
first written found 66 items; the confirmed ones reshaped it, and APP.md §2.1 carries the result:

- *Prove before replacing.* As drafted, a file's plaintext — `vault_id`, `modified_at`, a slot
  region trimmed to a recovery record, none of it authenticated before an unlock (R25, R35) —
  decided whether the working vault was retired, and "same vault, newer" replaced it with no
  confirmation. Now the file is staged, the ceremony proves it on the staged copy (a vault
  unlocks with its own way in, a backup completes setup), and only then is anything retired or
  installed. Every replacement needs the confirmed `replace`.
- *Never let the place be empty.* Retirement was a rename, so a crash between two renames left no
  `vault.eks` and a first-run screen offering to create over the user's real vault under a name
  the app never mentioned. Retirement is a copy under an `O_EXCL` name with a counter; the staged
  file is renamed over `vault.eks` in one step; the retired copies are counted on the status and
  offered on the first-run screen when nothing is kept.
- *Handles.* A rename cannot pass an open handle (Go opens without `FILE_SHARE_DELETE`), and
  handles exist in Broken and during a lock's asynchronous close; the install closes the handle
  and waits on `lockWG` as `openVaultFile` does, and the import ceremony ends Locked because the
  proving handle must close before the rename — the one cost of the design, paid once per import.
- *Open archives.* An archive open under the previous vault would have saved into the new
  registry, or dropped its receipt; an import or a replacement is refused while any archive is
  open.
- *Adopting an older backup.* It rolls the registry back to the export's date; the confirmation
  says so, and the retired copy keeps the newer records for a later `RestoreArchiveRecord`.
- *Tampered, Broken.* The drafted exemption was unreachable (Tampered is known only after an
  unlock, which the import never does on the current vault) and moot once every replacement is
  confirmed; Broken needs `Reopen` first. Both dropped.
- *`VerifyBackup` is not deferrable.* FORMAT §15 requires a backup to be verifiable before it is
  needed; deferring it made the only test of a backup a destructive one. It is in, on a staged
  copy, and inspection uses a staged copy too, so read-only media and files another process
  holds can be inspected (the draft opened the source read-write and locked it).
- *A recovery-only vault still opens.* Refusing every unlock on a setup-needed vault would have
  locked the user out of their archives; the recovery key still unlocks it, only a token or a
  password is refused, and "finish setting up" is the one action.
- *Missing vault, override, data folder.* A configured vault that cannot be opened is named on
  the first-run screen instead of "no vault yet"; an override set wins over a by-hand copy, which
  is said; `ENFOLD_DATA_DIR` now moves the vault too, and §10 says so.
- Smaller: the display name defaults to the current one for the same vault; the dialog says
  "another vault" only when a vault is kept; `FileInfo` counts slots by kind so a confirmation can
  say what is traded away; `CreateVault` over the vault kept here takes the same `replace` and
  builds the new vault as the incoming file, so a failed create leaves the old vault untouched.

**Reviewed once built (2026-09-07), 66 agents, 27 confirmed findings on about fifteen causes, all
fixed.** The one that mattered most: a create from nothing still wrote the keystore at its final
place before checking the ceremony's latch and cancel, so Cancel or a lock trigger during the
derive — a second of "deriving" with a live button — left a complete `vault.eks` whose recovery
key had never been shown, and the by-hand adoption at start then opened it as the user's vault
with a phantom recovery slot. Every create now builds the incoming file and installs it only after
the check, so a create cut short leaves nothing; the price is that a create ends Locked like an
import. Also: `BeginUnlock` refused the recovery key on a setup-needed vault, against §2.1 and
the lock screen's own button (only a token or a password is refused now); `VerifyBackup` had no
reachable entry — its button lived on the Keys page, hidden while locked, and disabled while
unlocked — so it runs from the lock screen; "use it where it is" bypassed the open-archives rule
(`OpenVaultFile` now refuses too) and its confirmation described a copy that was not made; the
replacement confirmation promised a dated copy of a vault kept elsewhere, which stays where it is
(the status says `KeptElsewhere`, the dialogs say so); a file at the vault's place that could not
be opened made "create a new vault instead" fail with `internal` (it is retired instead); the
replacing create installed even when cancelled; setup was not a mutation ceremony (shutdown did
not wait for its commit); an import's reopen failure after the rename left the override pointing
at the old vault (the settings are written before the reopen); a staged copy had no size bound; a
failed retirement could leave a half copy the status counted; "try again" on the missing-vault
screen renamed the vault; the create dialog's replace tick survived a cancel. Refuted: the shared
staging names (every caller runs under the ceremony gate).

**Verified again (2026-09-07, three agents on the fixes).** The create still lost its recovery key
when the reopen after the rename failed — install returned the error, the caller took "nothing
installed" and never minted the URL, while the file and the settings said otherwise. Install is
now total after the rename (settings first, a failed reopen becomes `MissingPath`, a failed
settings save a warning), so a create always shows its key and an import never fails after it is
in place. Also: `replace` guarded the one place only, so a create pointed at the vault's own file
elsewhere replaced it unconfirmed (any existing destination needs it now, and the dialog says
which); create and import were not waited for by shutdown (they are, as `commits`); a
recovery-key session on a setup-needed vault dead-ended every Keys action, since a mutation asked
for a password the vault could not have (a mutation falls back to the recovery key when no usable
token and no password exists); a verify started from the first-run screen never showed its result
(the outcome lives in the page's store, apart from the ceremony); the import dialog stuck in
in-place mode when the file changed to a backup; a phantom retired copy survived a failed rename;
garbage at the vault's place was retired and offered as a vault; the staging names could be chosen
as the vault's path and then removed by the next import; the copy bound was on the stat, not the
copy; inspections shared one staging name. Tests now cut a create short at its prompt and prove
the create's own recovery key opens the vault it made.

**And once more (2026-09-07, two agents).** A reopen that failed after an install dropped the
vault's path and name, so the screen said "nothing has been changed" over a vault just installed
(the path and name stay; only the facts drop); the tampered warning survived the import that is
its remedy (a fresh open clears it); a destination that could not be looked at was overwritten
instead of refused; imports staged under one shared name outside the ceremony gate (every staged
file has a fresh name now, and nothing of the user's is removed to make room); the Busy branch of
an open kept the previous vault's facts; the Busy exception in "use it where it is" also
disarmed the ceremony guard; install failures read as "internal" (a file in use is `vault.busy`,
with a short rename retry); an export could be written under a staging name and swept at the
next start (refused, as is any export into the data folder); the retired-copy counter sorted as
text. On the page: the create dialog asked to replace the missing file while creating at the
usual place (the destination decides now, in one place); a ceremony that failed without parking
was shown nowhere on the first-run card; the elsewhere wording named the wrong file; the page's
path compare was weaker than the core's (it is the same now, `lib/paths.ts`); "try again" kept a
dead ceremony's error; the strip's wording lagged a frame behind the event (the store derives it
from the events, `lib/outcome.ts`, with tests for both).

---

## 2026-09-07 — Passwords: at least 8; forms show their errors after the field is left; first hardware test

**Ruled by the user.** A chosen password — entangled or standalone — must be at least 8 characters,
BitLocker's rule; there was no minimum, and a one-character entangled password went through.
The core refuses a shorter one at submit (`vault.password_short`) and keeps the prompt; the page
judges it first. An existing password is never measured. An entropy estimate (SCOPE) stays
deferred; the minimum stands in for it.

**A design principle for every form, from the user:** a field with something wrong is marked once
the user leaves it — the underline turns red, and beneath it either the requirement already shown
turns red or a line says what is missing — the mark leaves the moment the value is right and
returns only after the field is left again; pressing the button marks every field that would
refuse, an empty required one with "This field is required."; the marks fade like everything
else. APP.md §6 has it; `lib/validate.ts` judges secrets and names; `SecretInput` and `TextField`
carry the behaviour.

**The first hardware test failed after the PIN, twice**, with "the YubiKey went away" and then
"the Smart Card service is not running" — and the log said nothing, because a coded failure was
not logged and the log was truncated at every start. What could be established from the code:
the second message is Windows stopping the Smart Card service when the last reader leaves (it is
trigger-started), which the waiting states treated as a failure instead of an empty reader set
— fixed, in the waits and in `Readers()`; the first is a PC/SC "removed or reset" answer after
the PIN was verified, inside the enrolment's management-key or generate step — which means the
key in 9d was *not* reused (a usable 9d key skips the PIN), so either the token does not report
the 9d key as usable (P-256, touch always, PIN once or always) or something reset the card
mid-way; piv-go holds the card exclusively, so a reset from another process is not the plain
reading. The log now appends across runs and records every ceremony's end with its code and
underlying error, and an enrolment logs what the token holds (slot, algorithm, policies, usable
or why not) and the slot it generates into. The next run tells.

**Verified (two agents).** The page's new checks had overreached in two places that are the
last resort: a recovery key typed with the dashes a word processor substitutes was refused
before the core, which accepts them, ever saw it; and a PIN shorter than six — an existing
secret, which the rule says is never measured — was refused although the card accepts one to
eight bytes. Both are the core's sets now, tested. A stopped Smart Card service was swallowed
silently for the wait's whole hour (logged once, and said on the screen after ten seconds); the
log rotation threw away the newest window (it is renamed aside instead); the enrolment logged a
second full read of the token; the error lines had no accessible name; a dialog reopened after a
refused press showed the press again; the recovery prompt said its rule twice.

---

## 2026-09-07 — The card is never left idle: what the second hardware test measured

The retest with logging said the same thing every time: `verify pin: transmitting request: the
smart card has been reset`, three times in a create and twice in an unlock, always after the PIN
prompt; once the reader itself vanished (`resource manager has shut down`, then `no YubiKey
reader`). The one create that succeeded had its PIN typed in four seconds; every failure had
stood at the prompt for eight to twenty. The user's read — "sort out when the key is held" — was
the right one.

**Measured on the test key** (`tools/pivtool idle`, `busy`; a YubiKey 5.7.4 with an empty PIV
module): an exclusive winscard connection with no APDU survives 5 s and is reset before 6 s; the
reset clears a verified PIN; a probe every 4 s (or 2 s) keeps it alive for as long as tried;
after this program's own close-with-reset the card reopens in 9 ms, so the "busy" answers seen
are not that reset's doing — Windows' own services taking the card for a moment after an insert
or a re-enumeration remain the likely cause, and an open now retries for two seconds before it
parks. Whether the reset is USB selective suspend or the resource manager's idle policy does not
change the rule and was not chased.

**Ruled.** The card is never held idle across a prompt (APP.md §2.2, DESIGN §11 trap 25): `piv`
releases the operation lock while a prompter waits and repeats an operation once after a reset
it meets, reconnecting to the same card; the ceremony probes a held card every 3 s while any
prompt stands, which keeps the connection alive and notices a pulled key within three seconds;
a key gone at any point in the token flow — the PIN, the touch, the management key — sends the
strip back to *waiting for the key* with a note, never to a failure. A second ceremony on a
card whose prompt stands is still refused; a probe is not. `pivtool idle`, `busy`, and `-slow`
on `selftest` stay in the tree as the way to measure this again on other firmware.

**Verified, and what the review found.** The headline behaviour did not work on the real path:
`piv` flattened the prompter's error under `ErrCancelled` with `%v`, and the adapter flattened
it again, so a key pulled during the PIN ended the ceremony as *cancelled* — the fake token
returned the prompter's error verbatim and the test passed. Both now wrap with `%w: %w`, the
fake wraps like the real one, and the adapter has a test for the chain. Around it: a reconnect
that failed left the Card on a closed handle (now *lost*: `ErrNoCard` from then on, nothing
to disconnect or reset), skipped the trap-24 probe (now probed, a busy card retried), and could
hand back the key `Overwrite` was meant to replace when the reset swallowed the GENERATE (now
compared with what was there); the operation resuming after its prompt could meet the probe
and fail as *busy* (the prober is joined first, and the resume waits for the lock); the
"waited for again" loops spun when a reader stayed listed without its card (a doubling pause);
the slot-change ceremonies' unlock half had no such loop (it does); a reset that leaked
classified as *internal* (now the key gone); a password enrolment held the unlocking key across
its prompt (released first); the enrolment's recover path could warn about a verified state a
pulled key took with it (a raw close); the away note stayed over *Probing* (cleared when the key
is back).

**Proved on the test key.** `pivtool idle -seconds 8 -verify-after` and `-seconds 12`: the PIN
verifies after the idle (it failed before the change). `pivtool selftest -default-pin -slow 8
-rounds 2`, run by the user with the touches: the PIN answered after eight idle seconds,
reconnect, VERIFY, touch, ECDH matching the software computation, twice — the exact sequence
every create and unlock had lost.

---

## 2026-09-07 — One vault means one: rebuild only on damage; the recovery key kept once more, and every showing saved, printed, or confirmed

Three rulings from the aftermath of the second hardware test, one of them a format change.

**No second vault from inside.** With one vault per user (2026-09-06), "Create a new vault…" on
the lock screen invited exactly the mistake that ruling forecloses. Create is a first-run action.
What a vault that exists can be is *rebuilt*, and only when its file cannot be opened — present,
and refused by `keystore.Open` — never when it opens, since a rebuild loses the keys the file
holds. The damaged file is retired beside the new vault as `vault-damaged-<unix>-<n>.eks`, a copy
the app names and never reads again; integrity checks and repair are for later, and that copy is
what they will work on. Absent is not damaged (import or create, as before) and tampered is not
damaged (the file opens; the cure is importing a copy).

**Saved, printed, or written down and confirmed.** The reveal offered one button, "I have written
it down", and took the user's word. Now a text file the core writes — after the user is told what
place to choose (safe, secret, reachable when needed; not the vault's folder, not a synced one),
through the native Save dialog and a bound call that carries a handle and a path, never the
digits; a print through `window.print()` and a stylesheet that prints the digits, the vault's
name and the date and nothing else — the one browser output the page invokes (previews stay
without one, DESIGN trap 13: they are decrypted content, and a recovery key is meant to leave the
machine); and "written down" with a second confirmation, because the first click is a reflex.

**Kept once more, under the VMK.** A recovery key shown once is a recovery key on paper, and the
one-vault ruling makes losing it costlier: there is no second vault to fall back on. So the
registry keeps every recovery key wrapped under `KWK_recovery`, a fifth child of the VMK (FORMAT
R3, R38, §7.6; `registry_version` 2, version 1 read and rewritten). The user's reasoning is the
design: *the session's cached keys cannot open it, so a protector must recover the VMK to
authorise a showing* — `RevealRecoveryKey` is a ceremony like a slot change, YubiKey or password,
never the recovery key. It adds no new principal (whoever opens the record holds the VMK), it
changes nothing about losing the keystore (§15), and the record's life is the slot's. The
design critique named the consequence the first draft glossed over: a VMK exposure — a memory
read, a leaked copy plus one of its ways in — is now a recovery-key exposure that no rotation
revokes, since rotation re-wraps a recovery slot from its public keys; the answer is replacing
every recovery slot, then rotating, and DESIGN §5, trap 11, FORMAT R28 and §15 say so now. A
slot made before the rule has no record and cannot be shown again until an unlock through it
hands the keystore the key (which writes the record) or the slot is replaced; the Keys page
says which. Two more things the critique fixed: the reveal holds the handle like a slot change
while the VMK is recovered (`keystore.Unlock` writes the handle's registry pointer; a save
racing it could lose its receipt), and the core — not the page — refuses a create while a vault
is kept (`vault.kept`), so the one-vault rule lives where every rule lives.

**Not done here.** Integrity checks on a damaged copy; an entropy estimate for a chosen
password; a "replace this recovery key" action in one step (add, show, remove, rotate
offered); a Rotate dialog that offers to replace the recovery keys on the removal path.

---

## 2026-09-07 — Third hardware test: a key proves itself before it is enrolled; wrong secrets stay in place; the recovery key's eight cells

The user created a vault with a YubiKey whose 9d already held an Enfold key from an earlier
try, and saw: insert the key, and the recovery key appears. No PIN, no touch. The log agrees —
"token holds 9d … reusing the key in 9d" — the enrolment reused the usable key and went on.
Correct by the letter (the vault needs only the public key) and wrong by any other measure: a
key in the reader was enrolled by being there, and the vault would have depended on a key
nobody had shown to work.

**Ruled.** Enrolling a key — reused or generated, at creation or later — ends with the key
proving itself: PIN and touch, over an agreement with an ephemeral key checked against the
public key that is about to be stored (`token.proof` refuses a mismatch). The PIN is not asked
twice when the management key's VERIFY still stands. A label left empty is the key's serial
number, so two keys never look alike. A wrong PIN is said, not only counted; a wrong password
or mistyped recovery digits are asked for again in place, with the reason — the ceremony is
not the thing that failed. The recovery key is typed into eight plain cells that check each
group's checksum as it is finished, take a paste whole, and keep the digits when the core says
they did not open the vault. The printed sheet has no page margin, so the browser prints no
header or footer of its own.

---

## 2026-09-07 — After the third test: a create ends in the vault; the show button only on a recovery slot; two questions answered

**A create ends Unlocked.** The user created a vault, proved the key, was shown the recovery
key — and was then asked to unlock. The 2026-09-06 ruling that "every create ends Locked" was
the price of building the keystore as an incoming file and installing it after the latch check
(the handle had to be closed for the rename); it was never a wish. Now the installed file is
opened with the recovery key just made and the session published, so the user is in with the
key's dialog over the vault; a lock trigger that landed meanwhile, or a file that will not open,
leaves it Locked with the key still shown. Imports keep ending Locked: the credential spent on
proving a file is not reused to publish a session.

**The Keys page shows "Show recovery key…" only while a recovery slot is selected**, rather
than disabled beside a YubiKey or a password.

**Why rotation is offline for every slot but an entangled one — the user's question.** The
asymmetric design holds: every slot stores a public key, and a rotation re-wraps the new VMK
to it with a fresh ephemeral key — no YubiKey needs to be present, no recovery key typed, no
password known. The one exception is a hardware slot with an *entangled* password: its wrap
key is derived from the ECDH secret *and* the password (`HardwarePreEntangled`), and the
password is stored nowhere — that is what entangling is for — so the re-wrap needs the
password. The slot that unlocked supplies it; any other entangled slot is left *stale* until
its password is given (`RewrapStale`), and the file cannot check a password without that
slot's token (there is no verifier for an entangled password, on purpose: a verifier would let
the password be attacked without the token), so the check that keeps a typo from silently
breaking the slot needs the token in the reader. `RotateOptions.SharedPassword` re-wraps every
entangled slot with the typed password, unverified, when the user says the password is shared.
A hardware slot without an entangled password, a standalone password and a recovery key all
rotate offline.

**Why the old create could reuse a key in 9d without any ceremony — the user's question.**
The same asymmetry: enrolling a key needs only its *public* key, which `GET METADATA` gives
without a PIN, and the keystore wraps the VMK to it with an ephemeral ECDH — the private key
on the token is used only at unlock. Nothing cryptographic required the key's cooperation to
enrol it, so nothing asked for it. The proof of possession added on 2026-09-07 is a rule, not a
requirement of the math: the vault must never depend on a key that was not shown to work, and
a key must never be enrolled without the hand that holds it.

---

## 2026-09-07 — Fourth test's details: a cancel during the touch, Remove greyed by the invariant, the rotate dialog's words, the dialog's ring

The user cancelled while the key waited for a touch and the ceremony sat at "touch your
YubiKey" until the touch came. Two things were wrong. The card call cannot be interrupted —
that was known and documented — but when the key gave up on its own the retry loop asked for
the touch *again*, without looking at the cancellation, so the cancel was never honoured. Every
retry loop now checks the cancellation before asking the card again, the state says
`Cancelling` from the click until the card answers, and the panel says so instead of showing a
button that did nothing. A cancelled ceremony leaves the page at once: no "Cancelled." to close.

Remove is greyed while the invariant would refuse it (`SlotView.Removable`, from
`keystore.Removable` on the plaintext facts), so nobody runs an unlock only to be told no. The
rotate dialog said a key not present would be re-wrapped later; the truth (the 2026-09-07 entry
above): every way in is re-wrapped here and now, and only a hardware key with an entangled
password other than the one that unlocked waits for its password. The dialog box took the
theme's focus ring when focus moved into it — a black frame, and one every dialog showed at a
Shift press; the box is not a control and wears none now, while its buttons and fields keep
theirs.

---

## 2026-09-07 — Measured: a PIV touch wait cannot be cut short from the host

The user cancelled Windows' own FIDO prompt mid-touch and the key's light stopped at once, and
asked whether PIV could do the same — and, if no command interrupts the wait, whether a power or
session reset could. Measured on the test key (`tools/pivtool touchabort`, YubiKey 5.7.4), with
the GENERAL AUTHENTICATE blocked on a touch nobody gave: `SCardCancel` on piv-go's context —
accepted, no effect, the transmit returned with the key's own 6982 at 14.3 s; `SCardDisconnect`
with `SCARD_RESET_CARD` from another thread — the resource manager queued it behind the
transmit, and both returned at 14.3 s; piv-go's own `Close` (disconnect and release) — the
same. FIDO's instant cancel is `CTAPHID_CANCEL`, a message of the HID transport; CCID carries an
APDU and waits for its answer, and PC/SC gives the caller nothing to send meanwhile. A USB-level
power cycle would need the device to be disabled and re-enabled through PnP, which is an
administrator's action and against the least-privilege ruling. Yubico's own SDK says the same
of its touch notification: "this call is informative only, there is no cancelling", and an
operation nobody touches for "times out" — the .NET SDK's KeyCollector guide
(docs.yubico.com/yesdk, *The KeyCollector's touch notification*, read 2026-09-07); Microsoft's
`SCardCancel` page limits it to "requests that require waiting for external action by the smart
card or user", which in practice is `SCardGetStatusChange`.

**Ruled.** The cancel is honoured when the card answers, and the page stops making the user
stand still for it: the touch step says *touch the key, or pull it out, to end the wait now;
left alone it gives up in about 15 seconds*. Both end the call at once — a touch completes the
operation, whose result the cancelled ceremony discards; a pull fails it — and the cancel then
wins over the "insert it again" note. `pivtool touchabort` and `Card.Interrupt` stay in the tree
as the way to measure this again on other firmware.

---

## 2026-09-07 — The pending touch outlives its ceremony; a pulled key's Win32 codes

The 14:42 build made a cancelled touch step say "touch the key, or pull it out, to end the wait
now". The user's answer: do not make anyone stand still at all. Cancel should take effect on
the page at once, and the backend should keep watching the call it cannot interrupt; and since
the person who typed the PIN seconds ago is the same person, the next request should be able
to use the touch the key is still waiting for. Their first sketch split it by the clock —
reuse under seven seconds, otherwise wait for the key to give up and start over — and then
simplified it: reuse the pending touch for as long as the key has not given up, continue it
once when it does, never more; a cancel merely sends the pending touch to the background, where
it ends the moment the key gives up. Two more rulings came with it: a normal unlock is bounded
too — one continuation after the key's own timeout, then an error, never an open-ended wait —
and every step must survive the key being pulled, including the case the fourth test hit: a
PIN typed, the key pulled, the PIN submitted, Windows half a beat behind, and the app showing
"something went wrong".

**Ruled.** The card call runs on its own goroutine, the *attempt*, owning the card, the token,
the handle it opened and its purpose (the kind, the file and the slot). A cancel disowns it and
ends the ceremony for the page immediately; the attempt stands as the pending touch. An unlock
from Locked that begins meanwhile adopts the pending touch of an unlock — the panel opens at
Touch, the key still blinking — and an attempt with an owner asks once more when the key gives
up (the PIN is still verified on the exclusive connection, so the light is back at once) and
never a third time; an attempt without an owner ends when the key answers, releasing what it
holds. The clock plays no part: "still pending" is a fact the code observes, "seven seconds"
would be a guess about the key's timer. Ceremonies that cannot adopt wait for the pending touch
with a note, and so does exit. The `Cancelling` state and its copy are gone. Rounds are counted
per attempt, so an adopted touch with two seconds left gets its one continuation and the total
wait for anyone is at most two of the key's own timeouts.

**Critiqued** (four Opus lenses, each finding refuted or confirmed by a second agent), and
three rulings changed before a line was written. *Only an unlock adopts.* The first draft let
any ceremony of the same vault adopt — "the touch authorises deriving the VMK; what is done
with it is the ceremony's kind" — and the security lens showed the escalation on an Unlocked
vault: cancel *Add a key*, and within the key's window one press of the blinking key shows the
recovery key to whoever is at the keyboard, where today the reveal costs its own PIN. An
unlock adopted by an unlock changes nothing but the wait; what remains — a cancelled unlock
finished by whoever is at the keyboard within two of the key's timeouts — is accepted, and
every lock trigger closes it. *A pending touch holds what its ceremony held.* Every guard that
serialised the one keystore handle keyed on the live ceremony, which the immediate cancel
clears while the attempt is still inside `keystore.Unlock` on that handle: registry writes,
the owed receipts a cancel pays at its end, `Reopen`, the next slot change all had to be told
to wait for the pending touch too — and the unlock's own file lock had to stop turning into
"the vault is open in another Enfold". *The two-round end is a finish, not a park*: the Keys
dialog's Close on a failed step forgets the ceremony on the page while a park keeps it in the
core, and a parked slot change refuses every save receipt until the process restarts. Smaller:
piv-go prints the facility's codes by text, never by number, so the piv-go table needed six
rows of text and the numeric parse serves only what piv-go cannot name; the "outside the
facility" rule is scoped to calls that address a card, since `ERROR_BROKEN_PIPE` from the
context means a remote session without redirection; a key whose PIN policy is *always* asks
for its PIN at the continuation, as its policy means; and `pendingTouch` has a line on the
lock screen only — elsewhere the next ceremony's own note says it.

**Verified** (four Opus lenses over the commit, each finding refuted or confirmed by a second
agent; the ownership, security and piv lenses found the handover race-free, the release
exactly-once and the tables right). Three things fixed after it. *The exit missed a touch it
had itself cancelled:* the shutdown waited for a ceremony's goroutine only when it held the
handle or was installing, but an unlock's card call becomes the pending touch only when the
cancelled ceremony disowns it, so a quit while the key blinked returned before there was
anything to wait for — measured 25 of 25 runs — and the card was left PIN-verified after all;
the exit now waits for any live ceremony's end first (microseconds, since a cancel is
immediate), then for the pending touch. *The adopted panel said "deriving":* the unlock's
Deriving step was set after the adoption as for any credential, and the adopted attempt, already
inside its card call, fires no touch prompt again, so the page showed a static ring where the
docs promise the touch; the Deriving step is skipped for an adopted attempt. *A backup import
whose enrolment proof was cancelled at its touch leaked the staged copy and its exclusive
handle:* the proof's attempt was built by hand without the ceremony's cleanup and holds no
handle, so nobody closed the copy or removed it; the ceremony's cleanup now rides on every
attempt it starts, run only when the attempt ends unowned, and the import closes the handle a
proof's attempt does not hold. Smaller: the lock screen's quiet line had taken the global
`.pending` banner class; §6 said a create waits where it is in fact answered with
`token.pending`.

The "something went wrong" was three return codes the log caught in the pull's half-beat:
`ERROR_GEN_FAILURE` from piv-go's VERIFY, `ERROR_BAD_COMMAND` from the package's own preflight,
and `SCARD_E_NO_SERVICE` when the release tried to reset a card whose reader had taken the
service down. Win32 codes come first, the `SCARD_` ones after; neither layer mapped the former
(DESIGN trap 26). Now every return code outside the `SCARD_` facility is the key gone, the
facility's own "not talking" codes likewise, and a reset that fails because the card is not
there is not a failed reset: a card without power holds no verified state.

---

## 2026-09-07 — The same VMK adopts: the user's ruling over the critique's

The first hands-on test of the pending touch was on the Keys page: cancel *Show recovery key*
at the touch, click it again — and the panel said "insert your YubiKey" with the note that the
key was still answering the cancelled request, then asked for the PIN. That was the critique's
ruling at work (the entry above: only an unlock adopts), and it was not what the user wanted:
"reuse" meant the normal flow with the PIN skipped, since the connection is still verified, and
one timeout's retry. Asked whether the escalation the critique named — cancel *Add a key*, and
whoever is at the keyboard presses the blinking key under *Show recovery key* — was the reason,
the user ruled that the boundary that matters is the process: so long as the PIN-verified,
touch-waiting state cannot be taken over by another program, the rest is acceptable. It cannot:
the connection is exclusive for as long as the touch is pending, and its release resets the
card. The exit waits for it (the entry above), so the process does not end under it.

**Ruled.** Any ceremony that agrees on this vault's VMK through one of its hardware slots
adopts the pending touch of any other such ceremony: an unlock from Locked, or the unlock half
of a slot change, an export or a reveal on the open vault. The panel opens at the Touch step;
the one continuation stays with the attempt. An import's, a verification's and a proof's
agreement are never adopted and never adopt. What remains — a cancelled touch finished for
another purpose on the same vault, within the key's window, by whoever is at the keyboard —
is written into APP.md §2.2 as the user's ruling, with the reasons: the vault is already
Unlocked or was seconds ago, the PIN was that person's, and every lock trigger closes the
window.

Smaller, from the same test: the strip said "two YubiKeys are inserted" for any number above
one; it says "more than one" now and does not count.

---

## 2026-09-07 — Takeover audit: nothing reaches a held card; the release gap was real and is closed

The user asked for an audit of the one thing the pending touch must never allow: another
process taking over a YubiKey that Enfold holds PIN-verified — during the PIN prompt's
keep-alive, inside the touch wait, as a pending touch — and getting a shared secret with one
touch. Two halves: an Opus workflow over the code and the PC/SC documentation (read-only), and
measurements on the test key with two new `pivtool` commands — `hold`, which holds the key the
way the app does and releases it one of four ways, and `probe`, which is the other process:
can it connect, in which share mode, and is the card verified for it (SELECT, GET SERIAL, the
retry-free empty VERIFY; nothing else).

**Measured** (YubiKey 5.7.4). While held — idle-verified with probes every 3 s, or inside the
touch wait — every `SCardConnect` from the other process answers `SCARD_E_SHARING_VIOLATION`,
shared, exclusive and direct alike. Killed while idle-verified, the card is unverified for the
next process within milliseconds; killed inside the touch wait, it stays "in use" until the
GENERAL AUTHENTICATE runs out (about 11 s more), then unverified: Windows resets a dead client's
card. **The release was the hole.** `Card.Close` was piv-go's leave-card disconnect followed by
a fresh shared connection that reset the card, and between the two the card is powered,
verified and unowned: a process spinning on `SCardConnect(EXCLUSIVE)` won that gap in one run
of five and held a PIN-verified card, while Enfold's reset failed with a sharing violation and
warned "still verified" — true, and too late. A control run with piv-go's close alone showed the
probe seeing `verified=true` at once, so the probe does see what it claims to.

**Ruled and shipped.** The reset goes on the exclusive handle itself:
`SCardDisconnect(SCARD_RESET_CARD)` on piv-go's own handle, reached by reflection (the
experiment's `Interrupt` already did it), so there is no moment in which the card is verified
and unowned; the two-step release stays as the fallback when the handle cannot be reached or
reset, and its warning stays with it. Measured after the change: fourteen runs of fourteen, the
spinning process connected only after the reset and found the card unverified — eight idle
releases, one across a touch wait's own end (the pending touch's release), six in the
experiment before it shipped. DESIGN §11 trap 27 carries the numbers; APP.md §2.2's claim that
the pending touch never crosses a process now cites them instead of asserting them.

**Audited** (three Opus lenses over the code and Microsoft's and Yubico's documentation, each
finding refuted or confirmed by a second agent). The holds were found sound end to end: the
exclusive connection and the transaction piv-go never ends refuse every share mode; nothing
without a handle can reset the card; the PIV applet has no path from the HID interfaces; the
keep-alive never lets go of the connection; the one reconnect follows a reset that already
cleared the PIN and preflights with another; `Open` resets, `dirty` is marked before the VERIFY,
a verified state this Card did not create is never trusted, and the exit waits. Five things
changed after it. *The long way's reset connection was the one connect never retried, and it
was shared:* a sharing violation at that instant ended the attempt with a warning, and a
program that won the gap in shared mode was co-resident while the card was reset; it connects
exclusive now, retries for two seconds, asks the card, then resets — measured, it recovers from
a momentary intruder and warns of a persistent one. *No service was taken as no power:* the
verdict now comes only after two seconds of retries, with the one unmeasured case (an
administrator's restart with the key in) written down. *The reflection could degrade in
silence:* `TestPivGoHandleLayout` pins piv-go's field layout in `-short`, and a release that
fell back is logged. *A reset declared from a return code:* on the exclusive handle the code is
trustworthy and the seventeen runs are its confirmation; the long way asks the card first. *A
recovered panic could leak the held card* — every flow closes its card before returning, so
only a panic leaves one — and a ceremony's end now releases whatever it still holds, a disowned
attempt having taken its card out of the ceremony's hands first.

---

## 2026-09-07 — Motion: one vocabulary, a full stop at the unlock, the native picker styled

The user asked for the app to move: the unlock, the lock, the pages, the rail's hover, press
and selection, the menus' drop and their fade, and link-style buttons that darken on hover.
The ruling is one vocabulary rather than a set of effects — tap 90, hover 120 in and 160 out,
leave 100, fast 140, move 220, settle 320 — with three rules: enter eases out and leave eases
in, nothing travels more than 12 px, one thing at a time. The unlock gets the one deliberate
pause in the app: the check stays 320 ms before the scene changes, so the ceremony ends with a
full stop instead of a cut. The menus asked for a decision: the system's own select popup
cannot be animated, but Chromium 135 made the picker a styleable in-page element behind
`appearance: base-select`, and the runtime here is 152 — so the `<select>` stays native with
its keyboard and its accessibility, and the list falls 4 px and fades in whole, which is what
the user asked for over a sliding reveal. The hover colour of a link-style button had been the
accent's *hover* shade, which is brighter; it is a darker ink now, the way a link is expected
to answer. `prefers-reduced-motion` zeroes everything, the settle included. APP.md §6, Motion.

---

## 2026-09-07 — The save bar, the settings page's shape, and what a toast is for

The settings page saved every control on change, squeezed its labels to a word a line when the
window narrowed, and kept its right column at a fixed width whatever the window did. The user
asked for a page that survives being narrowed, and for a save bar — edits staged and named,
saved together — designed after the one in their other project's admin pages (the per-user and
per-role permission lists), which an Opus agent read and summarised so that only its principles
crossed into this one: dirty is derived by diffing, never stored, so an edit put back by hand
un-dirties itself; the bar lives in flow at the foot of the scroll pane and sticks there, so it
can never cover the last row for good nor the rail; it names the changes rather than counting
them, folding what does not fit into "+N more…"; only editable fields count; validation is
delta-aware — a new problem blocks Save, an old one is shown; invalid explains itself in the bar
and at the field, never by a greyed button alone; busy is a label swap; success and failure are
not the bar's to announce; Discard is instant and unconfirmed.

**Ruled and shipped** (`SaveBar`, `store.settingsDraft`, APP.md §6 "The save bar" and
"Responsive"). The bar rises 8 px and fades in, sinks and fades out, frosted at 88% over a blur,
12 px above the pane's end with 12 px of the columns' own padding above it — the first draft
had the columns shrinking to the pane (`flex: 1; min-height: 0`), which put the in-flow bar
after a squeezed box and sent it to the middle of the page on scroll; the columns grow and
never shrink now. A *Vault* card with an editable name gives the invalid state a real case (an
empty name; spaces at the ends are allowed while typing, never a change on their own, and
stripped when saved). The page's two columns merge under 840 px of body and a row stacks under
470 px of card, by container query, so the window's minimum of 880 × 560 always holds every
control. Ctrl+S saves. The draft survives a visit to another page.

Three smaller rulings from the same session. *The recovery-record slider*: its label says the
number alone, close beside it, and the row's line says the rest ("Recommended: 3%; 0% is off.
Applies to exports made from now on."); a tick at the recommended value and an instant tooltip
were built and then removed — the label is the value, and a lone tick reads as a stray mark.
0% is accepted now: SCOPE calls the record optional, and the core had refused anything under 1.
The setting is stored but consumed by nothing yet — the record is a planned parity sidecar
beside exports, never a field of the envelope. *No "Settings saved." toast*: the bar leaving is
the confirmation. *Toasts*, for whatever comes next: only for what the page cannot say in
place, at the top of the window and centred on it — not on the content layer — dropping in a
few pixels as they fade and rising as they leave; the bottom-right ones of today move when next
touched.

---

## 2026-09-07 — Quit goes out of sight before it waits for the key

The retest: the pending touch works, and quitting from the tray while the key blinked froze
the whole window for the key's timeout — the exit's wait for the pending touch ran inside the
shutdown hook, with the window up. Killing the process instead left the key unreachable for
every program for the same fifteen seconds, which is not ours to fix: the card is executing
the GENERAL AUTHENTICATE, and the resource manager holds the dead client's connection until
it answers (trap 27's measurement). Ruled: Quit hides the window and the tray first, then
resolves, then waits unseen (`AwaitPendingTouch`), then ends — the process lingers invisibly
for at most the key's timeout, releasing and resetting the card itself. Logoff does not wait:
Windows resets the card at cleanup, measured, and gives a process only seconds anyway.

---

## 2026-09-07 — Revision 2: one entangled password for the vault, secrets under the VMK, the inspector, the recovery key's ID

With a test vault holding nothing, the user reopened the contracts while reopening is free.
Six rulings, one of them against the user's first proposal and one reshaped.

*The Archives page becomes the vault's inspector* — it already lists registry records, so it
grows rename, description, the full path with a foreign-platform reading, created time, KID and a
details modal — and gets the destructive pair the 2026-09-06 entry had deferred: *Forget key*
as a soft delete with thirty days' retention, and *Delete archive*, which removes the file only
after reading its envelope's `archive_id`. Confirmation is the archive's name typed; no
ceremony, since nothing cryptographic needs the VMK and the brakes are elsewhere. The raw
archive-key reveal the user sketched was declined: nothing consumes a bare key, and the sharing
feature's manual form is a one-record keystore export.

*The entangled password is one per vault* (the user's judgement: it is a password people keep,
and several of them add nothing against the threat it exists for). The chain is split so its
Argon2id key stands alone and is kept under the VMK — the user's idea — which makes changing it,
switching it, enrolling a key and rotating the VMK offline, and deletes the stale-rewrap
machinery. *Secrets get a section*: recovery escrow, the password's key and the retired VMKs,
under one `KWK_secrets`. *VMK history* replaces the user's per-backup wrapped VMK: a backup is
this vault at some generation, and knowing every generation's VMK opens every backup of one's own
without the sheet. *Merge records* with a selectable list joins *Import*. *Rotation keeps no
copy*: the user's new-file-then-rename with the old file retained was declined — the A/B flip is
already that, and a retained copy re-opens the removed slot; the dialog asks for a backup
instead. *The recovery key gets an ID* like BitLocker's, on paper and at the prompt.

Written into FORMAT.md Revision 2, APP.md §13 and DESIGN traps 28–30, to be critiqued and
then implemented; nothing is kept compatible with the vault that exists today.

**Critiqued 2026-09-07** — five Opus lenses (cryptography, keystore format, app flows,
documentary consistency, implementability against the code), 59 findings, 46 confirmed by two
independent verifiers each, 12 after merging, folded into FORMAT, APP and DESIGN in place; §18
keeps the rationale. What the critique changed:

- **§8's rotation was never amended.** The secrets section is under `KWK_secrets = HKDF(VMK)`, so
  a rotation must decrypt it under the retiring VMK, keep `K_P` for step 4 and re-encrypt every
  kept record under the new key — or the entangled key and the whole history are unreadable
  after one rotation and the offline promise dies with them. Steps 3 and 4 say so; the history
  record is keyed by the generation the retired VMK held; generation 1 is the first.
- **R38's orphan rule would have pruned `K_P` and the history** at any slot write (the shipped
  `pruneEscrows` keys purely on `recipient_id`). The rule is now kind 1's alone.
- **An export carries kinds 1 and 3, never `K_P`.** `K_P` survives every rotation, so an export
  that carried it would hand the vault's second factor to whoever leaks the export, for good;
  the history is the vault's memory of itself and a rebuilt vault keeps it — the synthesis had
  proposed leaving the history out too, and it was kept, since nothing in it opens more than the
  export's own recovery key does. An export's header says `entangle` 0 and adopting it chooses
  the entanglement afresh.
- **The header got its bytes**: 32, the layout in §6, R24's bounds only while `entangle` is 1,
  the salt redrawn at every change, authenticated by the derivation and by R25 — never by a
  record's AAD, which would have made the switch need the standalone password typed.
- **"Security is unchanged" was false**, and is replaced by what the design trades: `K_P` no
  longer depends on `H`, so the Argon2id grind can be run before the token's curve is broken —
  the same work, earlier — and a VMK holder reads `K_P` and keeps it across rotations. This
  overturns the 2026-08-30 property that `H` gates the Argon2id step, knowingly, for the offline
  operations; the answer to a VMK exposure is now three steps, the middle one a password change.
- **The slot invariant reads the header**: with the password on, every hardware slot's set
  contains it, and turning the password on in a vault of hardware keys alone is refused until a
  recovery key exists.
- **The retirements are complete**: R7, R29, R30, the deferred rotation, `rewrap_stale`,
  `rotation_pending` (reserved, written zero, ignored), the *Diagnosis* of §6.2 (a generation
  mismatch is tampering now, never "this key is behind"), `Keys.RewrapStale`, `SlotView.Stale`,
  `Escrowed`, `vault.no_escrow`, `EscrowOpenedKey`, `BeginEnroll`'s `entangle`; the record
  fields stay on the wire as zero so the record size and the AAD span do not move.
- **The secrets section is byte-exact**: a 53-byte AAD, the generation little-endian in `id`,
  zero tails checked, records sorted by kind then id, exactly one `entangled_key` iff the header
  says on.
- **The API carries the switch**: `VaultStatus.Entangled`, `Keys.EntangledState`, and the
  `Details`, `CheckFiles`, `InspectRecords` / `MergeRecords` / `DiscardRecords` methods the
  inspector and the merge needed and the first draft had not listed.
- **Merge** proves a file by trial against the current VMK and the history (the superblock's
  generation orders, never decides), needs a ceremony for this vault's VMK, asks the recovery key
  for a later-generation backup, keeps local values where the two differ and says so, and never
  takes the copy-describing fields.
- **Forget and delete** have four outcomes, of which only a proven match or a proven absence
  touches the record; an unreadable file never drops a key. The purge runs once, at the end of an
  unlock, on the write's own `modified_at`, so no Save on one archive destroys another's keys.
- **`last_path` is never auto-probed off local fixed volumes**, and `lastExportAt` is
  `{vaultId, at}` in the settings file, a convenience that gates nothing.

One finding was left as ruled: the critique argued the old entangled password should be asked
before a change or a switch-off, since a recovery-sheet holder can otherwise clear it and the
check is now one Argon2id run; it stays unasked, because whoever holds the VMK can enrol a way
in of their own already, so the check is friction and not a guard. The alternative is written
into APP.md §13 should the ruling be revisited.

## 2026-09-08 — Revision 2 implemented: kdf → format → keystore → app → frontend, the contract, the second clean-room check

The critiqued docs (ea0bc71) were implemented in one pass, layer by layer, without the user in
the loop: five Opus readers mapped what each layer had to change; one synthesis wrote a
cross-layer contract (exact Go and TS signatures, the order of work, per-layer acceptance tests,
the removals, sixteen open questions with rulings); two checks — one against the docs, one
against the code — found six blockers and ten majors in that contract before any code was
written (the merge had no way to re-wrap an incoming archive key; the forgotten-record write
guard blocked Restore; the rotation step omitted the identity re-wrap; the entangle mutations
took no R25 freeze; an export from an entangled vault would not have opened; `BeginEnroll`'s
real signature; `Keystore.size` already existed; `EncodeSlotRegion` and `Create`'s `u.kp`), all
folded into a binding amendments file. Then ten implementers in sequence (kdf and format in
parallel), each followed — at keystore, app and frontend — by an adversarial reviewer and a
fixer: 72 files, +8 197 / −2 053, every layer green under `-short`, `-race` for keystore and
app, `tsc`, `svelte-check` and the production build; `wails3 build` produces the exe.

What the reviews changed after the fact, worth knowing when reading the code:

- `AddSlot` no longer wraps from the handle's cached `K_P`: it re-reads the kind-2 record under
  the live registry (`entangleKey`), because a change on another `Unlocked` over the same file
  replaces the record without moving the generation. `Rotate` lost its entry guard for the same
  reason (step 3 recovers `K_P` from the section anyway). `AddFirstWayIn` refuses a vault whose
  header already carries an entanglement.
- A token unlock now sets `CeremonyState.Method` on every mutation ceremony, not only at the
  lock screen; the presence probe is bounded to one in flight (a global slot, reaped when the
  abandoned probe answers); a `DeleteArchive` claims its record under the mutex before the file
  work (`archive.busy` for anyone else); a merge whose incoming version KID this vault already
  holds under another archive drops that version rather than aborting; registry writes that
  change nothing no longer commit, so an unlock or a repeated Forget never restamps
  `modified_at` (R35).
- The Forget/Delete dialog works from a snapshot of the row (a forgotten record leaves the list
  the instant the write lands); both actions close an open archive first, asking about unsaved
  changes; the inspector's draft carries its baseline and re-stages when the record changes
  under it; the lock screen names the tampering cause.

Deviations from the contract the implementers recorded and that stand: the purge is its own
registry write beside the receipts' (the two fail differently), skipped when nothing would drop;
`archive.name_invalid` was minted (the only name code was a file's); a Delete of a record with no
`last_path` answers `archive.file_unreachable`; a record forgotten in the *other* vault merges as
an ordinary row (only this vault's forgotten records are unticked); `SetEntangled(true)` on a
vault already on behaves as a change; `HistoryGenerations` sorts numerically (section order is
by little-endian bytes); `ForeignRegistry.Tampered` is logged, not yet shown in the merge dialog
(no view type carries it — a 1.1 item); the rotate dialog's pre-selected backup performs the
export and then the rotation in one press. Two doc lists were widened by the code and are
recorded here as amendments: `CeremonyState` gained `Method` and `RecoveryID`, `FileInfo` gained
`RecoverySlots` and `Generation`, `VaultStatus` gained `TamperedReason`; APP §6's lock-screen
list lost `SwapKey`; `Keys.Export` of APP §3 is the shipped `Keys.ExportBackup`; FORMAT §6's
"the app's settings" became "the app's defaults" (no Argon2 knob exists). The merge's trial order
is APP §13's literal one — the current VMK first, then the history descending — over the
contract's "the file's generation first". A pre-existing form quirk was found and left: when one
prompt directly follows another, the new field shows "This field is required." before anything
is typed (`SecretInput`'s blur handling; not R2's).

**The second clean-room check** (SCOPE "Before the format is frozen" item 1, reopened by R2):
`tools/kdfvec` was rewritten for the new chain and the vectors regenerated; a test pins that
every unchanged subtree — the hybrid slots, the subordinate keys, the archive keys, the wrapped
VMK — is byte-identical to the pre-R2 file, so the regeneration touched only what R2 claimed. A
second implementer, given FORMAT §1, §3, §3.3 and §7.6 and `kdf-inputs.json` alone, re-derived
the changed subtree in its own Go module: **39 of 39 values identical**, including the 512 MiB
anchor. Its eight recorded ambiguities produced two sentences and one vector fix: (1) no
sentence said how a P-256 private scalar's bytes map onto the integer — §1's blanket
little-endian rule pointed one way, `crypto/ecdh` the other, and a wrong choice still yields 32
plausible bytes; R5 now says big-endian, as `NewPrivateKey` takes it. (2) `entangle_salt`'s width
was stated only in §6, which the implementer was not allowed to read; §3.1 now says 16 bytes.
(3) The vector file sealed its three secrets records under one key with one pinned nonce — the
exact GCM nonce reuse R22 forbids, modelled in the reference file; each record now has its own
pinned nonce and the vector test refuses a file where two coincide. The other five were
conventions of the vector file (which scalar is the slot's, the bare 65-byte public key, which
password the kind-2 record holds, what "retired generation" the single-generation file pins) and
one implementation constraint (NFC without `x/text`), all resolved by the inputs' own note.

Not yet done: the hardware test on the test key (the existing test vault is a registry-v2 file
with the old header and will be refused; it is recreated), and the user's pass over the new
screens.

## 2026-09-08 — After the first look at Revision 2: password and PIN on one card, the touch cached five minutes, a compression method per archive, the create flow

The user's first pass over the built Revision 2 changed three things on sight.

*Password and PIN on one card.* The lock screen asked the vault's password on card 2, then the
PIN, with a button reading "Unlock with YubiKey — the vault's password first" (truncated at the
width it was given). Ruled: one card, two fields, one *Continue*; the PIN goes first and is
verified at the card (`Card.VerifyPIN`, a VERIFY with no touch) before the password is used, so
a wrong PIN marks only the PIN field and moves nothing else — the password stays in its field as
an ordinary editable masked input and goes out, in whatever state it is in then, only once the
PIN is right; a wrong password, known only after the touch, empties and marks its field and asks
no PIN again; the button says *Unlock with YubiKey* whatever the switch. The user's own
refinement: "verify the PIN first; if it is wrong nothing needs to go further, the UI does not
jump, the password just stays there." The
security question was raised and settled by the user's own reading: the password already lives
in the core's attempt for the whole unlock and `H` beside it, so a PIN held a second longer in
the page moves no boundary; the core's prompt contract is untouched and the PIN still crosses
the raw channel once.

*The touch cached, with a clock.* A wrong entangled password can only be known after the touch:
`K_P` is under the VMK, and a verifier for it is deliberately absent (it would be an offline
oracle for the password, the one thing R2's trade did not give away). So a wrong password cost
a second touch. Ruled: the attempt caches the touch's `H` per `epk` in the token wrapper it
hands the keystore, releases the card, and re-derives on the retry — no PIN, no touch — for
**five minutes from the touch**, whatever happens in between; the deadline zeroes the cache and
ends the ceremony at the lock screen's first step. The user's own framing of what the clock is
for: not a defence against reading memory (five minutes is a window nothing stops) but a bound
on how long a hardware-verified state can be used without the password. No keystore change:
the token the keystore calls is the app's, and memoising `ECDH(epk)` there is where the cache
belongs.

*A compression method per archive.* "Store files raw (for already-compressed media)" was a
checkbox whose label described the default — already-compressed media is detected by sampling
and stored raw by itself, per file (DESIGN §9) — while the bit itself was trap 8's "every file
raw". Ruled: a five-segment method control (Store · Fast · Normal · Better · Best, Normal
preselected) in the create dialog, the level carried in the record's policy bits 3–5 so that
every writer of the archive compresses the same way (the level was a writer property until now,
recorded nowhere). What the compression layer offers, checked: four presets (the pure-Go zstd's,
not 22 levels), a match window from 1 KiB to 512 MiB that stays automatic by preset and is not
recorded (DESIGN trap 16 caps the reader at what this program writes), and the small-file
dictionary whose threshold the Settings page already carries. The window may become an advanced
per-archive choice later; not now.

*The create flow.* Name and File side by side let the path be chosen before the name and fell
back to `archive.efd`. Ruled: name and method first, then *Create* opens the native Save dialog
with `<name>.efd` prefilled in the folder last used; a chosen path where a file already exists is
refused in place — the archive layer creates with `O_EXCL`, and Enfold never overwrites a file it
did not make, whatever the dialog's own replace prompt said — and the user is asked for another
name.

Hardware: the user recreated the test vault on the test key with the entangled password on; the
unlock chain (password, PIN, touch, adoption) runs on the real key.

**Landed the same evening** (one Go implementer, one frontend implementer, one reviewer: four
minors, applied). Facts the code fixed that the ruling left open: the ceremony's own VERIFY sets a
one-shot *fresh verify* in `internal/piv` that the next agreement spends — a key whose PIN policy
is *always* consumes the card's verified state on one operation, so "skip the VERIFY when
verified by us" alone would have looped on a second agreement; `Card.VerifyPIN` answers
`Verified: true` with no retry count (a verified card answers the empty VERIFY with success, the
rule `PINStatus` already states); a matched key the design refuses is refused straight after
probing, before any PIN is typed; the deadline reaches the page on `VaultStatus.Note`, since the
ceremony is gone by then; the clock is armed only when the attempt carries a password; the
enrolment's proof of a *new* key keeps its in-`ECDH` PIN (it is not a way in and asks no
password). On the page the card's body is keyed on what it shows, not on the step, so the
password field survives the PIN prompt's departure and the password prompt's arrival; the
auto-send judges the field first (an empty password is marked, not sent). `Shell.SaveFile` gained
a directory argument (Wails' `SetDirectory`). A test-runner flake was found and is not the code's:
about one `go test -short ./internal/app/` in four ends with a `TempDir` cleanup error under
`%LOCALAPPDATA%\Temp` — the on-access scanner holding a freshly written file — and is clean with
`TMP` pointed at the excluded folder.

## 2026-09-08 — The archive index is a tree: directories are records, files hang off them by id

The user, seeing *Create folder* specified as a page-side fiction, asked whether the index was
"the object key of an object store" — it was: every file record carried a full path and folders
were the prefixes, so an empty folder could not exist, a folder's modified time was lost, a
folder rename rewrote every record under it, and a created folder vanished at save unless a file
landed in it. The user ruled that a standalone folder must exist on its own, indexed without
reference to any key. Two shapes were weighed: explicit directory records beside path-keyed files
(RAR's and zip's: a directory entry per folder, files still by path), which keeps two sources of
truth and still rewrites every file on a folder rename; and a real tree, files and directories
hanging off a parent by id, the path a derived thing. **The tree was chosen**: one record per
directory (`dir_id`, `state`, `parent_id`, one-element `name`, `modified_at`, `revision`,
`last_writer`), `parent_id` on every file record, the root implicit at id zero, R20 reduced to
one path element with the 4096-byte bound moved to the joined path, and R39 stating the tree's
invariants — live parents only, sibling names unique under simple case folding (the platforms
this project extracts to would fold `A.txt` and `a.txt` onto one file), no cycles, depth at most
255, a deletion tombstoning the subtree in one write, nothing ever derived from a prefix.
`index_version` becomes 2 and 1 is refused: only test archives exist. What it buys beyond the
user's ask: a file or folder move is one record (`Move` ships, closing the deferred "folder move
as one operation"), folder times round-trip through extraction, and a future file-level merge
sees a rename as one field change. Compression, the STREAM layer, the free map and the keystore
are untouched: the change is the index alone — the encrypted metadata, as the user guessed.
DESIGN trap 31 records the model that was left behind. To be critiqued, then implemented.

**Critiqued the same night** (three Opus lenses — format, archive layer, app — 14 findings
confirmed by paired verifiers, 12 after merging, all folded in place). What it added: R39 now
binds identities (no all-zero id, no repeated id, one id space across both tables, fresh random
ids never recycled) and states the reader's order (identities, then chains, then files); the
writer holds the same invariants — the index encoder refuses a bad tree, and every staged change
that names or re-parents a record re-checks the whole subtree beneath it for depth, joined path
and case-folded sibling uniqueness against the transaction's own index; R20's element bound is
255 UTF-16 code units (what NTFS counts) rather than bytes, with the APFS/ext4 cost written down;
a directory's `modified_at` is the folder's own time and no change advances it (R32 gained the
directory tombstone's rule; SYNC notes that a directory has no tie-break); R33 keeps the record
order and every id under compaction and rotation. In APP: the root is the all-zero hex id at the
bound boundary and is never acted on; `Crumbs` runs root-inclusive down to the folder shown and
the page draws the breadcrumb from it alone, walking it upwards when a folder it stands in went;
`AddFolder` enters an existing directory whatever the policy and kinds that differ never replace
(`file.kind_mismatch`); `Move` and `Rename` are pre-flighted whole against R39 (`file.exists`,
`file.move_into_self`, `file.tree_bounds`, `file.not_found`); a deleted directory is one overlay
entry and one greyed row, nothing may be staged beneath it, and un-staging a staged directory
returns what was moved into it; extraction plans a set ordered parents-first, creates folders
because their records are live, sets times deepest-last on folders it made, and fails whole at
plan time on a containment miss; the file drop carries a directory id, never a path.

**Landed in Go, 2026-09-09** (format → archive → app, one Opus implementer each, a reviewer with
six minors, all fixed): `index_version` 2 with `DirRecord`, `ValidateName` in UTF-16 units, R39
in six passes (identities, fields, chains memoised, files, folded siblings by a fold-key map that
a test pins against `strings.EqualFold`, joined paths) run from both `Encode` and `DecodeIndex`;
the archive layer's `Tx.AddDir` / `Delete` of a subtree / `Rename` / `Move`, `Children` and
`Path` in place of the whole-name lookup, `Compact` and `RotateKey` carrying the tree; the app's
merged view rebuilt per call, `Page` with root-inclusive `Crumbs`, `CreateFolder` as a staged
`Tx.AddDir` returning at once, the policy rules, `Move` refused whole, extraction's ordered set
with times only on folders it made, the drop by `dirId`. Two deviations worth the record: a
file's `modified_at` is now the content's clock — an add, a replace and a deletion advance it, a
rename or a move does not (§11 amended; Explorer does not move a file's date on rename either);
and the archive layer checks sibling uniqueness by scanning both tables with `EqualFold` per
staged change, O(records) — fine at this size, a per-parent map if a folder ever holds tens of
thousands. `CheckNames` offers a directory by a trailing `/` on the name. The frontend follows.

**And in the page, the same morning** (one implementer, a reviewer with nine findings — one
major: a failed listing left the page standing in a folder it never showed — all fixed): the
folder is a directory id, the breadcrumb is `Crumbs`, a created folder is the core's record, rows
carry folders with the sum beneath, a deleted folder is one greyed row, Rename and Delete take
folders, a drag onto a folder row or a crumb is `Move` with its refusal under the target,
*Extract all* sends the root id, the drop carries `dirId` and a per-path kind the shell now stats
(`main_windows.go`), the collision dialog names kinds and greys *Replace* across kinds, and the
per-item outcomes have a surface the docs had not named: a *What happened* dialog, opened for
failed and skipped items alike (a skipped subtree is one line for its top and would otherwise
vanish; narrowing it to failures is one line). Left open, small: *Extract all* is no longer gated
(the file count cannot say whether the tree holds anything — a record count on `ArchiveStat`
would), Move has no keyboard path, and the mock's Discard does not revert a staged move.

## 2026-09-09 — Operations commit at once (the staged model retired), the extract dialog, print detection, the details modal

The user, on the built tree: "is the confirm-changes mechanism really necessary? WinRAR has
none — an add needs only a Cancel, a delete a warning that says what is being permanently
deleted." Weighed: the staged model bought a multi-step batch published in one commit and a way
to drop it whole, at the price of a dirty state nobody else's archiver has — a pending bar,
*Save changes* and *Discard*, an expiry prompt with extensions, a per-archive cap, an overlay
with a precedence vocabulary and un-staging rules, and the question "did I save?" that every
archiver answers by never asking it. Per-operation commits keep every atomicity the format
gives (one commit per operation, all-or-nothing, a crash leaving the previous index) and cost a
few milliseconds of index rewrite per rename. **Ruled: each operation is its own transaction**,
committed at its end; *Cancel* aborts an add or a replace (the bytes it wrote lie in extents the
committed free map still holds free); a delete asks first — files and folders counted apart, "a
folder takes everything beneath it", "this cannot be undone" — and then commits; the overlay,
the pending vocabulary, `Save`, `Discard`, `KeepOpen`, `archive.expiring`, `Dirty`, `CapAt` and
`SessionAlive` go; `ArchiveStat.Records` arrives so *Extract all* can be greyed on an empty
archive. Progress is by bytes within the file in hand — the bar had stood still for a whole file.

*Extract all* extracts from the root straight into the chosen destination, never into a folder
it makes on its own. The user asked whether the picker could prefill the archive's name when the
user makes a new folder; the native picker cannot (Wails exposes no such hook and the dialog's
*New folder* is its own), so the page gets an extract dialog with an editable destination
prefilled from `lastExtractFolder`, *Browse…*, and one action that appends `\<archive name>` —
WinRAR's destination field, in effect.

The recovery key's saved and printed name is `Enfold Keystore Recovery Key <ID>.txt` — the
user's choice, BitLocker's own shape ("BitLocker Recovery Key 5A58C3B0-…") — the key's ID and
not the vault's name; the same string heads the file and the sheet, and `document.title` carries
it into Print to PDF. Whether a print was
submitted or cancelled — which BitLocker knows — is read from the print spooler: the shell
snapshots every local printer's jobs before `window.print()` and polls for three seconds after
`afterprint`; a new job, even one that later fails, is a submission, and only an unreadable
spooler falls back to the second question.

The details modal loses its column of *Copy* buttons for selectable values with a right-click
*Copy*, wraps the hash, reads *N/A* for an all-zero one, scrolls as a whole, and shows at least
three key versions before that table scrolls. Sorting is still to be designed.

**Landed the same morning** (one Go implementer, one frontend implementer, a reviewer with twelve
findings — one major, a doc sentence the code had outrun — all fixed): `beginOp` / `commitOp` /
`abortOp` make every operation one transaction with the receipt-owed path untouched; the overlay,
the pending vocabulary, `Save`, `Discard`, `KeepOpen`, `archive.expiring`, the cap clock,
`Dirty`, `CapAt`, `SessionAlive`, `DirtyArchives` are gone from Go, the bindings, the page, the
CSS and the mock; `Records`; the byte counter is a forward front (the compression probe samples
the middle and the end of a file, so a sum would over-count); an extract streams through a
temporary beside the target and is placed exclusively, so the bar moves within a file there too;
`internal/spool` reads winspool.drv behind an interface with a fake, and the shell's
`PrintBegin` / `PrintEnd` wrap `window.print()`; the reveal names its file and its print
"Enfold Keystore Recovery Key <ID>" and treats a submitted print as done; the details modal is
selectable with a right-click *Copy*. One consequence worth knowing: with `SessionAlive` gone,
everything on the Archive page but *Delete archive…* works while the vault is locked, as APP
§2.3 has always said it should, the receipt owed until the next unlock.

## 2026-09-09 — The first outside audit: Codex over the tree and the immediate-operations code

The user added a skill that runs the OpenAI Codex CLI as a read-only subprocess, for a second
pair of eyes that shares none of this project's history and is billed elsewhere. First use: one
Astra pass (its own subagents failed to launch, so one review rather than three) over the tree
and the immediate-operations change against FORMAT §11/R39 and APP §2.3/§3/§5 — 27 files,
eleven findings, spot-checked here before anything was done with them. What held, and what it
changed: (1) a crash inside the rotation's in-place envelope rewrite left an archive that would
not open, because the reader decoded the envelope before trying any key; R33 now makes an
envelope that fails its checksum *absent*, the reader trying the registry's keys against the
index and rewriting the envelope — the envelope was never trusted for anything else (§10).
(2) An add or a replace had no deferred abort, so a panic recovered by the operation runner
left the archive's transaction open until it was closed; every operation now aborts on any exit
that is not a commit. (3) A cancel arriving after the last check while the commit ran was
answered as if it had worked; `CancelOp` now says the operation is already committing. (4) The
idle clock looked at readers and compaction but not at an operation still walking its source
before taking the archive's lock; it now holds for any running operation. (5) The extract's
temporary name carried the whole target name plus a suffix, so a valid 255-unit name could not
be extracted; the temporary is a short random name beside the target. (6) The spooler watch took
one snapshot at `Begin` and looked again only at `End`, so a job that spooled and finished while
the print dialog stood was missed — it now polls from `Begin` on and only stops three seconds
after `End`; and a snapshot that stalls is abandoned at the deadline rather than holding the
dialog. (7) R20's "no control character" was implemented as C0 and DEL only; `unicode.IsControl`
now, which adds U+0080–U+009F. (8) Compaction checked for a cancel between files but not inside
a multi-gigabyte one; it checks per chunk. Two findings became doc corrections rather than code:
the reopen check compares `last_seq` only (a file ahead of the record is a lost receipt and is
adopted; the size is not compared, an aborted tail being normal), and the shutdown's receipt
write is bounded by nothing but a healthy disk. One was declined: that a rotation leaves both
keys usable is already what R33 says, and the reader tries them in order.

## 2026-09-09 — Space comes back; the print watch retired; the reveal asks nothing twice; the strip counts

The user's second pass on the built app. *A deleted file left the archive its old size* — an
emptied archive of 8 GB — because deletion is a tombstone and cryptographic erasure and the space
waited in the free map for the next add or a compaction, with R31 forbidding a writer to truncate
what the previous superblock still references. Ruled: a cancelled add truncates at `Abort` (it
already did); a commit that frees the file's tail is followed at once by an empty commit that
truncates it — the quarantine lasts exactly one commit and that is the one — and any commit
leaving the free space at or above 64 MiB and a quarter of the file is followed by a compaction
the core runs itself, shown as *Reclaiming space* and cancellable. WinRAR rewrites the whole
archive on every delete; a quarter is where rewriting the live three quarters is worth the
holes.

*The print watch is retired.* It lagged two seconds on a cancel and could not see *Save as PDF*
at all: the WebView's print dialog writes that PDF itself and no spooler job exists. The
alternative — the system print dialog through the WebView2 API, which does spool every path —
is not reachable through Wails, and the user chose the simpler contract over more machinery:
pressing *Print…* counts as done, and so does *I have written it down*, with no second
confirmation for either ("a user set on clicking out is not stopped by a second dialog; the key
can be shown again, and the vault is empty at that moment"). `internal/spool`, `Shell.PrintBegin`
and `Shell.PrintEnd` go; DECISIONS keeps their story.

*The strip says what it does*: "Adding — adding" becomes *Adding 3 files*, `OpView.Items`
carrying the planned count; a count of one is singular everywhere ("1 files" was on the status
line).

**Audited the same evening by Codex** (read-only, two subagents, 20 files): the mechanism holds
under honest `Sync` — retirement keeps recovery, `seq-1` makes no tie, one torn write leaves a
valid copy, both copies cannot tear from one crash, the relocated index carries the right AAD
and key, the free-map hash matches, an interrupted follow-up never double-frees. Two findings
kept: a read-only handle opened beside a writable one in the same process registers its readers
with itself, so a trim could truncate under it — the archive layer now refuses that second open
(`ErrBusy`) where the path is spelled the same way, that table being keyed on the canonical
path, and R31 says why every other reader fails closed instead: a foreign process's, and a
read-only handle under a `\\?\` spelling, a junction or a short name, which no lock catches
either; and the whole rule assumes an honest `Sync`, now written into R31. Two test gaps closed:
the crash seam tears and fails writes rather than skipping them, and an interrupted follow-up is
retried on the same handle. One prompt lesson: telling Codex "do not run any tool" stops it
reading files; say what it may not do.

## 2026-09-10 — In-place compaction; an open archive has no timeout; extract's destination and its conflicts; the red Delete

The user's third pass. **In place** (asked as "why can't we modify the archive file directly?"):
a file system cannot cut a hole out of the middle of a file, but a live extent can be moved
verbatim — same DEK, same nonces, same tags, the chunk AAD naming no offset — into an earlier
hole by an ordinary commit that updates `data_off` and quarantines the old extent (FORMAT R40).
Reclaiming space is that, one extent per commit from the front, the tail cut as it comes free,
cancellable between commits, resumed by the next; the cost is the live bytes after the first
hole (WinRAR's whole-archive rewrite in the worst case, far less usually), no second file, no
double space. It runs after any commit that leaves a hole of 64 MiB or more before live data;
the whole-file rewrite stays as the explicit *Compact*. The auto-reclaim of 2026-09-09 (a
rewrite above a quarter) is superseded.

**No timeout of the archive's own.** The user: the per-archive idle expiry "cut work off for no
reason"; an archive should stay open while it is in Enfold's management view, vault locked or
not; leaving the view closes it at once and destroys the DEKs, unless something is reading it
over HTTP (a state for the later external-player playback, with its own timeout then, and the
server bound to 127.0.0.1 and nothing else, written into §4 now); *Close archive* stays as a
kill switch that drops readers too; and the Archives page's own operations — Verify, Compact,
Rotate key — never ask the user to open the archive first: while the vault is unlocked they
unwrap the key with the session's KWK, open for the operation, and close. DESIGN §10's earlier
rule ("otherwise keep-it-open bypasses the session timeout") is reversed and the reasoning
recorded there. The locked banner is one plain line.

**Extract.** The destination is prefilled by one rule: *Extract all* → the archive's own folder
plus a folder named after the archive, created when the extract starts; a selection → the
archive's own folder, no extra level. The "+ a folder named after the archive" action and
`lastExtractFolder` are gone. `replace` is the default policy — the wizard default of every
archiver, and the case the first design forgot — written through the temporary and placed over
the old file in one move; `ask` is the drag-and-drop shape: extract what collides with nothing,
then one dialog for one conflict (Replace / Skip / Compare both files) or for many (Replace all /
Skip all / Let me decide for each file), the compare list after Windows Explorer's — the type's
icon, *Files from the archive* against *Files already in the destination*, dates and sizes,
ticks on either side, "Skip N files with the same date and size" at the foot. Drag out of the
archive does not exist yet; when it does, it is `ask` with no dialog first.

**The Delete button** was the accent's green with red text; it is a filled red button.

**The outside critique** (Codex Astra, read-only, over R40 and the lifetime rules, the same
day) held up on every anchor it cited and amended both. On R40: the destination is pinned
*wholly before the source* and never appended (the archive layer's allocator prefers an exact
fit anywhere and appends otherwise, so it cannot be used unchanged); a file no hole before it
holds is skipped, not a stop; a commit moves as many extents as fit in a 64 MiB budget rather
than one — a qualifying hole in front of ten thousand small files would otherwise cost ten
thousand index rewrites; the trigger measures **bytes the file system gets back**, at least
64 MiB and a quarter of what must move, instead of the size of a hole, since a hole filled
behind a file that cannot move returns nothing and a small hole at the head of a huge archive
is not worth the rewrite; a hole freed by the commit just made is still under R31's quarantine
and an unchanged transaction publishes nothing, so a run that wants it begins with the empty
commit that publishes it; an extent a reader holds is left for the run and the plan is
re-made when the last reader closes; a cancel is honoured per chunk of the copy, as the
whole-file Compact already does, so *Close archive* is never behind a 100 GB copy; the free
map is always appended and the index sometimes, so a move commit can leave the file larger
until the follow-up cuts the tail, and a reclaim that fails after the user's edit committed
says "saved; reclaim incomplete"; and "fewer holes" was the wrong word — a move can turn one
hole into two; what a move does is lower the live data. On the lifetime rules: "closed the
instant it stops being looked at" was not true — an unattended desktop, or a window minimised
to the taskbar, keeps the page mounted — so DESIGN §2 and §10 now say what is conceded (the
review also caught that the window's close, which destroys it, sent no Leave at all, and the
user then supplied the rule that was missing: **closing the window exits every archive;
minimising to the taskbar does not** — `Core.LeaveAll` on the window's close; and when the
external player exists, a close while something is still being served to it asks once first,
marked in APP §2.3 and §4 for that day); and "for the readers
alone" had no boundary — the whole handle and its token stayed — so leaving with a body in
flight is a **draining** state (no new request, no operation, closed after the last body), and
the external player of a later version gets a file-scoped lease with a deadline bound to the
playback the user started, never to requests. The critique's own subagents failed to launch
("no thread with id"); it read the fourteen files itself, in 101k tokens.

**The review experiment** (the user's idea: let Codex Astra review what Opus implemented,
then an Opus fixer, instead of Opus reviewing Opus). The batch implementing the rulings above —
two Opus implementers, Go then frontend, no Claude reviewer — passed every gate; Astra then
read the diff (56 files, 175k tokens) and returned thirteen findings, every anchor of which
held: four high — *Leave* only cleared the page's flag, so the token, `findArchive` and every
page method went on working after the page was gone; closing the window sent no *Leave* at
all, so an archive's keys outlived the window (the user's rule, above, followed); *Close* took
the operation mutex first, so the kill switch waited behind a running add; and `Archive.Close`
tore a compressed reader down under a `Read` that was inside it, a nil dereference in
`compress.Reader.classify` — seven medium (two openers of a closed archive both reached
`archive.Open` and the second got `archive.busy`; a locked page could mount a handle the core
had opened for a verify; the page's own walk of `Page` to find a conflict's record id stopped
at the first 200 rows; the *ask* bookkeeping raced the operation's end and lived in the
component, which the lock scene destroys; *Extract all* kept an old "not twice" exception; the
pre-check under *ask* against "a stat after the refusal") and two low (new copy outside
`strings.ts`; dark brown ink on the dark theme's red button) — and judged eight of the
implementers' recorded deviations sound in a line each. All but the pre-check finding were
fixed (that one is an accepted deviation, the doc reworded: the collision may be seen by the
pre-check, the stat comes after it, nothing is written over on a stat's word): draining is the
page's flag alone — unmounted, every page method answers `archive.not_open` and the preview
URL 404s, *Open* re-mounts, the last body closes; `Core.LeaveAllArchives` on the window's
closing event; *Close* drops the token, cancels the operation, kills the readers
(`Archive.DropReaders`) and only then waits for the handle; a `Reader` mutex held across
`Read`/`Seek` and taken by the kill, a.mu before r.mu everywhere; an opening reservation the
second opener waits on; no mount of an unmounted handle while locked; `FileOutcome` carries
the record's `ID`, `Size` and `ModifiedAt` and `OpView` an extract's `Policy` and
`Destination`, so the conflict question is derived from the store's operations, survives the
lock scene and needs no walk; *Extract all* always adds its level; the batch's copy in
`strings.ts`; white ink on a saturated red in both themes. Verdict: this shape found more, and
graver, than the Opus-reviews-Opus batches did — a panic and a kill switch that waited would
have reached the user — and it costs Anthropic usage nothing; keep it. Astra's own subagents
fail to launch every time ("no thread with id"); it reviews alone and still delivers.

## 2026-09-10 — In-place compaction implemented (R40)

The archive layer gained the move step behind three calls: `PlanReclaim`, a dry run on the
in-memory free map that answers one commit's moves — each live extent, in offset order, into
the earliest published hole wholly before it that holds it, held sources and oversized files
skipped — and a run-level estimate beside them; `Publish`, the explicit empty commit that spends
R31's quarantine; and `MoveExtents`, one commit of moves whose destinations are taken exactly
from the transaction's pool by a new `tx.take` (never the allocator's first fit, never an
append), the record pointed at the copy before a byte is written, the copy made in 1 MiB chunks
with the cancel checked at each, the source freed and quarantined by the commit like a deleted
file's data, and the whole thing aborted — originals live, copies in free space, the file no
larger — on any error. A plan a commit can no longer honour (a source moved, a hole retired or
held) is refused whole with `ErrStalePlan` and writes nothing; the app plans again.

**The adversary's pass** (an Opus reviewer told to break it and to leave its proofs as tests,
`adversary_*_test.go`): a crash at every write of a move commit — the copy chunks, the index,
the map, the flip, each step of the follow-up — every cancel and torn write per chunk, a Close
under the copy, a reader opened mid-copy or holding the old tail, a damaged free map, and every
hand-crafted destination a plan would never choose (held, quarantined, live, the losing index,
another move's source or destination): no data loss, no state Open cannot read, R31's fallback
kept, the map's invariants held on the handle and on a reopen. Two low findings: the
tail-returned estimate over-promised by one index and one map when the move commit's own index
first-fitted just above the new last live byte, in the run the follow-up would cut — fixed at
the root, a move commit and a publishing commit keep their index out of that run, appending it
if no other hole holds it (the file ends tighter, not looser, for it); and the contract's app
sketch worked one plan through several commits, which the layer refuses safely — the app plans
afresh for every commit instead, since the commit's own index takes a hole and the sources just
moved are in quarantine.

**One commit deep was not enough.** The app implementer, building the worth rule on the plan,
found that `[hole S][S][S]` — the user deletes the first of three equal files, the case that
started all this — plans one move with nothing returned (the third file still anchors the tail),
so the run would never start although two commits give S back. The plan's estimate is now the
whole run, simulated commit by commit on the free-space model: a source one commit frees is a
hole for the commits after the next, an empty publishing commit inserted where the quarantine
demands it, bounded and answering for what it walked, never above what the real run returns
(proved by running three layouts to convergence against the figure). The worth rule weighs
`RunTailReturned` and `RunBytesToMove`; the one-commit figures stay for the tests that pin them.

**The app** plans before each commit, publishes when the plan wants a quarantined hole, moves
up to 64 MiB of ciphertext per commit, pays the registry receipt after each, does not quiesce
readers (a move needs no drain; whole-file *Compact* keeps its), is triggered by every
operation's commit and by the last reader's close, is not gated on the session (a commit is
not; the receipt waits), ends as cancelled under *Cancel* or *Close archive*, and otherwise
reports `Returned` — the strip's toast says *Reclaimed 1.2 GB* — or `archive.reclaim_incomplete`
("Saved. Reclaiming space did not finish."), never touching the edit's own outcome. The
constants — 64 MiB returned, a quarter of what moves, 64 MiB per commit — sit in one rule,
lowered by the tests. `RotateKey` rewrites the file and needs no reclaim.

**The outside review of the implementation** (Codex Astra over the diff, 38 files, 214k tokens):
no high finding; three medium, two low, all real. The run-level estimate simulated each step's
whole move list while the app commits a budgeted prefix and plans again — a budget changes which
holes coalesce and so which files move, and on a contrived layout the real run moved five times
what the estimate weighed — so the dry run now takes the same budgeted steps, and the run stops
by itself, cut short rather than failed, if what it has moved exceeds four times what has come
back plus what the fresh plan promises. The last-reader trigger was lost when an extract's own
readers were released by a bare decrement or when a reader closed during another operation —
every operation's end now re-plans, a reclaim's own end never (a cancelled reclaim is resumed by
the next qualifying commit, not by itself). The first extent of a commit ignoring the budget is
kept as the necessary exception — an extent larger than the budget cannot be split and would
otherwise never move — and written into §2.3. The mock's overlapping reclaims and the
"Reclaimed …" line outside `strings.ts` were the two low.

## 2026-09-10 — The list: `..`, four columns, checkboxes, sorting; dialogs stay put; names the destination refuses; no lock scene first

The user's fourth pass, from the built app. **The heading is the archive's name alone** and a
button to the root; the folder is shown by the list itself, whose first row in a folder is `..`
— up one level, pinned under every sort, no checkbox, never selected, not counted by Ctrl+A.
**Four columns** — Name, Size, Type, Modified — *Stored as* leaves the list (the details keep
it); a folder shows no size; the type is drawn from the extension, *File* for none; columns
give way from the right when the pane is narrow. **Checkboxes** head every row and the header
row of both lists, the header ticked exactly when every row is and never a third state; ticks
are the selection, cleared by a blank click, by entering a folder or going up, persisting
nowhere. Two selection faults: Ctrl+A did nothing, and Shift+click after a blank click still
ranged from the row that click had deselected — the anchor is the last row clicked without
Shift, a blank click clears it, and with no anchor Shift selects the clicked row alone. The list
keeps one row's height free under its last row, scrolled or not. **Sorting**, at last: the
core's, through `Page`'s `sort`, so paging stays in one order — name ascending by default,
directories always first, a general collation (the root locale of `x/text/collate` with `Numeric` and `IgnoreCase`,
checked on the machine: digits by value, accents secondary, case ignored with a code-point
tie-break, scripts in the Unicode collation's order and Han by code point — since a
locale-specific order would differ by machine and the user asked for the general way); Modified opens descending and
breaks ties by name in the name column's direction; Size opens ascending, folders (sizeless)
first; Type is the extension, folded, none first. **Dialogs stay put**: any dialog with a choice
ignores a click outside it — "they have a Cancel" — and only a dialog whose one action is
*Close* dismisses on the backdrop. **A window opened from the tray while the vault is unlocked**
flashed the lock scene: the page now draws nothing until the first status and then the right
scene. **Names the destination refuses**: R20 already holds Windows' rules on the way in, so the
name itself always fits a Windows volume; what cannot be known beforehand is the destination's
path length and the volume's own limit (SMB, exFAT), so an extract that meets a refusal reports
that file as `name_refused` with the rest written — never a failed batch, never a hang, and no
name loop runs unbounded (keep-both numbering stops at the first refusal that is not "already
exists") — and the page asks per file: *Shorten* (the extension kept, the stem halved until the
volume takes it), *Rename…*, *Skip*. **A drag out of the window** onto the desktop or Explorer —
the user's ask, the `ask` policy with no dialog before it — waits on the feasibility answer
(WebView2, OLE, a cgo-free data object); the ruling stands, the mechanism is the next entry.

**The critique** (Codex Astra over the rulings as written, 21 files): fifteen findings, all
about the specification and none against the decisions, folded in: the heading superseded two
older sentences that still drew a breadcrumb and made crumbs drop targets (now the `..` row and
the heading are); one signed key could not carry the Name column's tie-break direction (`sort`
is one or two signed keys); the root collation puts the Han extension blocks after the unified
block, so "by code point" is said per block; the per-archive `Seq` rule read literally rejected
a second page at the same revision (a request token tells stale from same-revision, and a newer
`Seq` restarts the listing, ids surviving); *Shorten* and *Rename…* had no way through
`Extract` (a `names` map for this extract only); a refused folder name and a refused
destination root had no path (the folder's subtree reported once; the root is the operation's
error); *Shorten* had no floor and no answer to a refused temporary (one rune; the path is the
problem then, *Skip all like it*); the header's tick and Ctrl+A meant "every row" over a list
that pages (`Children` answers the folder's ids); the Archives pane and its ticks could name
different archives (the pane shows the last-clicked row while ticked, else the sole ticked
one); `..` needed a focus model apart from selection; Ctrl+A needed a scope; the blank boot
needed a failure exit; the extension and folder-in-Size rules needed saying; and Esc needed an
answer in a dialog without *Cancel* (*Skip*, never *Replace*). The keep-both numbering loop was
checked and already stops — the fear of an unbounded loop was not the code's defect.

**Drag out, researched** (Codex Astra, 40 sources read, the same evening). Feasible, and the
shape is settled: WebView2 does turn a page's drag into a native OLE drag, but a page cannot
supply file *contents* — only strings, URLs and existing paths — so the host must own the
drag: the page cancels `dragstart` and hands the gesture to a bound call, the shell calls
`DoDragDrop` on the UI thread with its own data object. Wails beta.16 has drop-in only (issue
4648 asks for drag-out) and never calls `OleInitialize`; pure-Go COM objects with
`syscall.NewCallback` vtables exist (go-ole's example, zzl/go-com's `IDataObject`,
`IDropSource`, `IStream`) and are building blocks, not a finished exporter. Two mechanisms:
`CF_HDROP` over files staged in %TEMP% — what 7-Zip's FAQ describes and its `PanelDrag.cpp`
does, WinRAR reportedly the same — or **virtual files**, `FileGroupDescriptorW` plus
`FileContents` streams Explorer pulls at drop time, what Windows' own zip folders hand over;
Enfold takes the second, since the first writes plaintext where nobody asked. What the user
asked for — ask about a conflict only when it occurs — is exactly what Explorer does for a
virtual-file drop, with its own *Replace or Skip Files* dialog; but it is Explorer's dialog and
Explorer's decision, the source never learns the destination or a per-file receipt, and there
is no "skip and ask at the end" to be had. Streaming gigabytes works when the stream is real
(a reader-backed `IStream`, not a buffer filled in `GetData`), the STA must not be stalled by
a slow read, and `IDataObjectAsyncCapability` lets Explorer copy in the background while the
archive stays open. Ruled: design written into APP §3; a stand-alone prototype under
`tools/dragproto` first — a window that drags a synthetic 5 GiB virtual file onto the desktop
— before a line of it enters the shell.

**The list, implemented, reviewed, fixed.** Three Opus implementers in sequence — the core
(`sort.go`: one collator per Core behind a mutex, keys computed once per row; `Page` renders
only the window it was asked for; `Children`; `Extract`'s `names`; the two refusals; keep-both
exhaustion as `failed`/`file.exists`), the Archive page (`lib/sort`, `lib/selection`,
`lib/paging`, `lib/refused`, the `..` row, the checkboxes, scroll paging, the refused-name
dialog), then the dialogs, the boot gate and the Archives list — and the outside reviewer over
the diff: fifteen findings, two high, ten of the implementers' deviations judged sound. The two
high were the selection's: unticking the last row (Ctrl+click or its checkbox) emptied the
selection but kept the anchor, so the next Shift+click ranged from a row the user had just
deselected — the very fault the user reported, in a second dress; and a select-all's
`Children` reply arriving late overwrote a selection the user had changed meanwhile, which a
Delete could then act on. Both fixed (an anchor lives only while something is selected; a
selection generation guards the reply), with the medium ones: a sort change mid-scroll mixed two
orders; a superseded load-more left paging dead; the destination de-duplication used
`ToLower` where R39 folds (now the index's own `FoldKey`); a refused folder name was reported
as a path refusal, hiding *Shorten*; conflict re-issues dropped the names a *Shorten* had
chosen; the Delete question counted only loaded rows (`Children` now answers kinds too and
the deletion acts on exactly what the question named); `Stat` replies had no token or `Seq`;
the Details modal's Copy menu fought the focus trap; every open dialog answered Esc (a dialog
stack — the topmost owns Esc, the trap and the backdrop); the ceremony's Esc now cancels it;
focus after going up survives paging; an event before the first `Status` reply opened the
boot gate (it no longer does; the gate is the call's completion); and `name,-name` is accepted.
Recorded so the next reader knows which of the list's rules were the reviewer's.

**The prototype, built** (`tools/dragproto`, the same night): a Windows-only, cgo-free program
— a small window; `OleInitialize` on its locked thread; on press-and-move, one `DoDragDrop`
with a data object of its own — `IDataObject` and `IDataObjectAsyncCapability` on one
refcount, `IDropSource`, `IEnumFORMATETC`, and an `IStream` per file, every vtable a pinned
block of `syscall.NewCallback` addresses looked up by the bare `this` and never dereferenced.
The files are synthetic (a 5 GiB one and a 1 KiB one by default; `-n`, `-size`, `-delay`,
`-folder`), their bytes a pure function of the offset generated by a producer goroutine into
a bounded buffer, so `Read` only drains and a seek is free. Every struct layout was compiled
against the Windows SDK (FILEDESCRIPTORW 592 bytes, cFileName at 72) rather than recalled, the
descriptor the encoder writes was parsed back in C, and the generator was cross-checked in
Python. An independent read-only ABI audit found the vtables, IIDs and HRESULTs exact and four
real defects — a four-byte over-read of a caller's HGLOBAL in `SetData`, a latent double
release of the async self-reference, a goroutine-stack address handed through
`syscall.SyscallN` (which keeps the value alive but does not move it to the heap — and a
`make([]uint32, 1)` did not either, the race detector proved; a `//go:uintptrescapes`
wrapper does), and unlocked reads at teardown — all fixed, with the stream pointer now left
at the *start* by default (the documentation and Raymond Chen's sample leave it at the end,
which is now the experiment), `FD_UNICODE` set, and released objects kept as tombstones so a
call on one is logged rather than misattributed. Two things only a real drop can answer, and
the user's run is for: whether a relative path in `cFileName` makes Explorer create the folder
(the documentation says nothing), and Explorer's own dialog and progress on a collision, a
cancel and a stalled read.

**The first drop** (the user, the same night, Windows 11): a 1 MiB virtual file dragged onto
the desktop arrived whole with its SHA-256 matching; then the 5 GiB file and the 1 KiB file
together — both whole, 5 368 709 120 bytes exactly. What the log says Explorer does: it asks
for `IMarshal` and `IAgileObject` and, refused, marshals every call back to the apartment that
made the object; it runs the asynchronous protocol (`SetAsyncMode`, `StartOperation`,
`EndOperation(S_OK, COPY)`); it requests the contents stream once *before* the drop and never
reads that one, then again after the drop and reads that in 256 KiB chunks — 20 481 reads,
7.3 s, about 730 MB/s — then `Seek(0)` and release; a second request for the same file is
therefore the ordinary case, not a corner; peak working set 85.5 MiB, nearly all of it the
runtime and the window. Every call, the whole copy included, landed on the drag thread — the
STA — which in Enfold would be the WebView's UI thread pumping reads for the length of the
copy; so the next experiment is an *agile* data object (the free-threaded marshaler
aggregated, `IAgileObject` answered) so Explorer's worker calls the streams on its own thread,
which is also the shape the decryption goroutine wants. `Get-FileHash` over 5 GiB merely took
its time; it was not a hang. The second drop taught the other rule: dropped again onto the
desktop, *Replace* chosen, Explorer began the operation and asked for the stream — and never
read a byte, because that `Get-FileHash` still held the destination open and Explorer's copy
engine stalled before its first read, its progress dialog unable even to cancel; the user then
closed the prototype, which destroyed a data object Explorer still referenced. So: a target may
ask for a stream and then not read for an arbitrary time, and the source must keep the object
and everything behind it alive until `EndOperation` — never tear down what the target still
holds; a close while an operation is in flight waits, and a forced one leaves the streams
answering an error rather than freed memory. The prototype gained both, and an `-agile` mode
for the next round.

**The second research pass** (Codex Astra, 52 sources, the user's question after the drops:
is the stream route a marginalised one, and does 7-Zip's temp-file route have its reasons?).
What held up: the bar-only dialog is the long-standing behaviour of the descriptor/contents
route — practitioners reported it in 2012 and 2023 — and no descriptor flag upgrades it
(`FD_FILESIZE` supplies the size, `FD_PROGRESSUI` asks for a dialog, neither chooses its
style); the modern dialog is reachable only by handing Explorer real shell items — a namespace
extension with `ITransferSource` — a far larger build. The protocol is documented, undeprecated
and still implemented by Chromium and Outlook, but nothing shows Microsoft investing in its
dialog. Cancellation on that route is cooperative — the shell checks between chunked reads, so a
`Read` that blocks hangs both sides — and errors stop the copy without a *Retry*; the stall
behind a locked destination could not be attributed to any documented limit (Windows answers a
sharing violation at once) and needs a diagnosis, not an assumption. The temp route, read in
7-Zip's own source: an empty temporary directory is made *before* `DoDragDrop`, early
`CF_HDROP` requests get it, the button's release switches the paths and arms the extraction,
which then runs inside the next `GetData` — because targets ask for `CF_HDROP` during the hover,
and 7-Zip's comments name the consumers that broke on it (Sticky Notes, Edge). Its costs, as
its authors document them: staging space and a second pass of writes (a same-volume move can
spare the payload copy), plaintext that must outlive the drop — Igor Pavlov acknowledges that
deleting the staging after `DoDragDrop` breaks consumers that open the paths later, Chromium
schedules its staged downloads for deletion at reboot, WinRAR scavenges files older than an
hour at its next start, PeaZip documents residue after a crash — and no ordinary deletion is a
secure erasure. `CF_HDROP` reaches more targets (Notepad++, plain `WM_DROPFILES` receivers),
though browsers accept virtual files too (Chromium materialises them itself). Advertising both
formats does not keep the choice ours — the target picks, and Mozilla fixed a real
format-selection bug of that kind. The reviewer's position: the virtual route for Explorer and
the desktop, an explicit *Extract…* as the escape hatch, never `CF_HDROP` silently beside it;
reconsider only if plaintext staging became acceptable. The decision is the user's and is
pending.

**Ruled: the staged route** (the user, after the second pass): "B — fewer problems ahead, and
the common practice. A drag is one or two files the user wants out quickly; double the space
is nothing next to a copy that stalls half-way and starts over. For the whole archive I use
*Extract all* and name the folder." APP §3 rewritten: `CF_HDROP` by delayed rendering, a
staging folder per drag under `%LOCALAPPDATA%\Enfold\drag`, hover-time requests answered with
the paths the files will have, the extraction run by the drop's own request after release,
`EndOperation` the signal to delete, an hourly scavenge at launch and delete-at-reboot as the
backstops, `DROPEFFECT_COPY` only, the object agile so the extraction never runs on the
WebView's thread. The prototype's virtual-file work stays as the record of what that route
does. A third research pass — 7-Zip's source and both archivers' communities on the staging
folder's lifetime — precedes the shell integration.

**The third research pass** (Codex Astra, 62 sources, 7-Zip's source read at 26.03): what
the archivers actually do. 7-Zip makes an empty `7zE<8 hex>` directory straight under
`GetTempPath` before `DoDragDrop`; hover-time `CF_HDROP` requests get that directory's path,
the button's release arms the copy, and the next `GetData` runs the extraction with a modal
progress window inside the still-outstanding `DoDragDrop` — but `GetData` returns the names
whether the extraction succeeded or not, and the directory is a stack object whose destructor
makes one recursive delete and never retries, with no launch-time sweep anywhere in the
inspected code (24.04 added a manual "delete temporary files" window). Its comments name the
consumers its early placeholder broke — Sticky Notes refuses a path that does not exist, Edge
caches the early name — and its tracker holds the other side: deleting after `DoDragDrop`
took the files from under FileZilla, VMware, a configuration dialog that kept the paths for
minutes, TeraCopy; Igor Pavlov: "another program can't open input files in that case". 7-Zip
advertises copy *and* move and returns *move* without touching the member — a same-volume
move of the staged copy is faster and keeps creation time — and refuses to delete members on
a drag (a rewrite, a risk). WinRAR stages under its configurable temporary folder and deletes
externally used files, drags included, on a later run once they are an hour old, because
"external applications may still need them". Chromium stages a dragged download and marks it
for deletion at reboot — which needs an administrator and deletes a directory only when empty
— and ignores the result; it is an attempt. Windows' asynchronous protocol ends *the target's
transfer* at `EndOperation`, not every later use of a path, and no trace proved Explorer
negotiates it for a `CF_HDROP` source at all; a currently unlocked file says nothing about
whether a consumer reopens it later. Folded into APP §3: a manifested folder per drag under
`%LOCALAPPDATA%\Enfold\drag`; final paths at hover, the extraction at the post-release request,
a failed extraction failing `GetData`; copy and move allowed with move preferred and the record
never touched; deletion at once for a drag that handed nothing out, at `EndOperation` when
negotiated, and otherwise by a scavenge at launch and every ten minutes of manifested folders
older than an hour, retried with bounded backoff — never delete-at-reboot; the explicit
*Extract…* for what staging serves badly. The reviewer proposed a 24-hour grace; the user's
threshold stays WinRAR's hour, plaintext being the cost that matters here.

**The findings kept.** The three passes' raw findings — positions, every finding with its
confidence, its verification status and the URLs the researcher opened, and its own gaps —
and the measured facts of the two real drops are in `docs/research/drag-out.md`, so that later
work (the external player, other shell integrations) consults them instead of running the
research again (the user's ask).

**The staged route, measured.** The user's `-hdrop` round (research file, last section):
Explorer asks for `CF_HDROP` during the hover and takes future paths; it negotiates the
asynchronous protocol for a `CF_HDROP` source but never calls `EndOperation`, so the design's
"delete at `EndOperation`" is a courtesy and the scavenge is the mechanism; with the object
agile the extraction ran on Explorer's thread; a same-volume drop was a move, instant, nothing
left to clean; and the *Replace* of an existing 5 GiB file stalled before Explorer touched
the source — the second time, once on each route — while `avp.exe` scanned, which points at
the machine's on-access scanner holding a large file's open and not at either route; an
attribution test without the prototype is the next step. Nothing in the design changes; the
scavenge's place in it is confirmed.

**Confirmed, and one more rule** (2026-09-11, 00:40). With Kaspersky paused the same
*Replace* of the 5 GiB file completed at once and the process exited cleanly: the stall was
the scanner's on-access read of a large unfamiliar file, held across Explorer's open, on
either route. The user, having watched the pause before Explorer starts: "this must be why
WinRAR and 7-Zip still show an extraction progress window after the drop — we need one too,
the strip we show when adding files would do; the user must never think we froze, and the
whole window freezing is out of the question." APP §3: the staging phase is the operation
strip — *Preparing 2 files*, by bytes, *Cancel* — the request running on Explorer's thread so
the window stays alive, a cancel failing the request so Explorer abandons the drop.
The strip has two phases (the user's refinement): *Preparing N files* with its bar and
*Cancel* while the request runs, then *Awaiting Windows Explorer*, no bar, once Explorer has
the paths — cleared when the staged files are gone, when `EndOperation` comes, or when they
have been read and left alone for five seconds. And the scanner is not a release concern: "a
5 GB .bin dropped into a temporary folder and immediately operated on looks suspicious, and
getting locked for a scan is normal behaviour; real use will not see such a file, and if it
does, that is the user's to sort out."
**One gesture** (2026-09-11, before the integration): a page's HTML5 drag cannot become the
native drag and two drags cannot run at once, so the list's rows stop being HTML5-draggable
and every press-and-move starts the native drag; a release over Enfold's own window is a
self-drop — nothing extracted, the folder deleted at once — that the page turns into the
`Move` it always was, by the ids it kept in flight; a release anywhere else is the drag out.

## 2026-09-11 — Drag out, integrated

Two Opus implementers (the first interrupted by the session's limit and resumed on top of its
own half — it had lifted the prototype faithfully; four things were wrong or unfinished),
then the outside review, then two fixers. **What landed.** `internal/dragout` is the
prototype's COM runtime made a library: the pinned vtables, the refcounts with the
self-reference ledger, the free-threaded marshaler aggregated and `IAgileObject` answered,
the `CF_HDROP` data object with `IDataObjectAsyncCapability`, the enumerator, the drop source
that arms the extraction on the button's release and tells a self-drop from the window under
the cursor, the staging folder — `manifest.json` at its top, the items under `items\` so that
a record called `manifest.json` cannot touch it — with its state machine (hover requests
answered with the paths the items will have; the first request after the release runs the
caller's extraction inside `GetData` and fails it if the extraction failed or was cancelled,
even a cancel that lands during the last file's placement), the watch over every item, files
and folders alike, and the four ways *Awaiting* ends (gone, `EndOperation`, read then idle
five seconds, released then five seconds with no read — the last because a target that keeps
the paths for a later read would hold the strip for ever, and because Explorer never calls
`EndOperation`: the async self-reference is given back when the target's own references
reach zero), the cleanup table (a drag that ended without a drop and wrote nothing deletes at
once; an accepted drop waits for the target to let go) and the scavenge that asks the
exclusive-open question before it deletes, deletes the manifest last and rewrites it when a
delete fails half-way, backs off 1 s, 10 s, 60 s, never enters a reparse point, and logs no
file name. `InitOLE` runs on Wails' main thread through `InvokeSync` before the window exists
(WebView2's later `CoInitializeEx` on that thread becomes an `S_FALSE`, not a mode change) and
`UninitOLE` runs last at shutdown, skipping `OleUninitialize` while a target still holds an
object. `Shell.DragOut(archiveID, ids)` asks the core for the plan, runs `DoDragDrop` on the
main thread — whose modal loop keeps the WebView alive — and returns `{SelfDrop, Extracted,
Effect, Folder, OpID}` when the drag ends. The core's operation of kind `dragout` holds the
archive like any operation (Leave lets it finish, Close cancels it), runs the existing extract
machinery with `replace` into the items folder as its callback, and moves through `dragging`
(the hover, nothing shown), `preparing` (*Preparing N files*, by bytes, *Cancel*) and
`awaiting` (*Awaiting Windows Explorer*, no bar) to a `DragResult` of `self_drop`,
`cancelled`, `refused`, `moved`, `ended`, `idle` or `failed`; the scavenge runs at `Start` and
every ten minutes. The page's rows are no longer HTML5-draggable: a press-and-move past four
pixels calls `DragOut` once with the selection, and a drop that lands back on a folder row,
the `..` row or the heading is the gesture's own only when it carries the gesture's own names
(or paths under the drag's folder), in which case it is the `Move` the list always had; any
other drop in that second is a real file's add. The mock plays the phases on a clock.

**The review** (Codex Astra, 36 files, 348k tokens): fourteen findings, three high — a
dragged record named `manifest.json` would have overwritten the manifest (hence `items\`); a
sweep whose delete failed half-way took its own manifest with it and so abandoned the
plaintext for good (hence manifest last, rewritten on failure); and a real file dropped
during a self-drop's one-second grace would have moved the previous selection (hence the
drop's identity by names and paths) — and eleven medium, all applied: the async
self-reference hid the target's release; directory-only moves never completed; a cancel
during the last placement still succeeded; release-and-idle was undocumented (now the fourth
way, in APP §3); cancelled drags waited for the target; the sweep deleted before asking; the
per-drag trace lacked the formats and the bytes; and four in the mock. Nine of the
implementers' deviations were judged sound. Not yet measured in the real window: that
WebView2 hands the page one `File` per `CF_HDROP` path for a self-drop whose paths do not
exist — if it did not, a self-drop would be judged foreign and the *Move* would silently not
happen; the first real self-drop says.

**The first launch died.** Every start after the integration ended at "application created":
`OleInitialize` had been wrapped in `application.InvokeSync` before `Run`, and in Wails
beta.16 the platform implementation that `InvokeSync` dispatches through is made inside
`Run` (`a.impl = newPlatformApp(a)`), so the call dereferenced nil on the main goroutine —
a panic no handler sees in a windowed process. The implementer had read the module and
concluded the opposite; the module says otherwise. The main goroutine is the main thread
(Wails locks it at package init), so `InitOLE` is called directly. Recorded as the kind of
claim to check by running, not by reading, when the run is allowed.

**The first real drag** (2026-09-11, the user, a video from an archive onto the desktop): the
file arrived, but the *Preparing* bar was never seen — the staging was over before the strip
had appeared — and, having answered *Skip* to Explorer's conflict dialog on a second drop, the
user found the operation hung at *Awaiting Windows Explorer*: Explorer neither read nor moved
the staged file, never ended the asynchronous operation and never let the data object go, so
none of the four ways to clear could fire. Two rules (APP §3): the strip is on the screen from
the drag's start, labelled *Extracting N items* throughout — label alone during the hover, the
byte bar with *Cancel* during the staging, the bar replaced by *Awaiting Windows Explorer* at
its end (the user's shape: "first the label, then the bar, then swap the bar for Awaiting");
and a fifth way to clear: thirty seconds after the drop with the files never opened and the
object still held is *idle*, the folder left to the scavenge.

**The synchronous drop** (2026-09-11, the user's question after the hung *Skip*: "so we
cannot know whether Explorer finished? Skip should count as finished. WinRAR holds its own
window while Explorer copies, keeps Explorer's dialog in front, and treats the extraction as
done whichever the user chose — replace, skip, or a cancel half-way — then releases the
window"). That is Windows' own rule, and the asynchronous protocol was the mistake: a source
that offers `IDataObjectAsyncCapability` lets the target copy in the background after `Drop`
returns, and Explorer, having taken that offer, never called `EndOperation` and after a *Skip*
neither read, moved nor let the object go — hence four heuristics and then a fifth to guess
when a drop was over. A source that does not offer it obliges the target to finish inside
`Drop`: `DoDragDrop` returns when Explorer has copied, or the user has answered its dialog, or
cancelled, with the real effect. So the capability goes; *Awaiting Windows Explorer* is the
time inside `DoDragDrop` and ends when it returns; the result is *moved*, *copied*,
*cancelled*, *refused*, *self-drop* or *failed*; the staging folder is deleted at the return
when the release was over an Explorer window or the desktop (the window class recorded at the
release) or a move took everything, and kept for the scavenge otherwise; the watch, the idle
clocks and the release-tracking are gone. The main thread sits in the modal loop meanwhile —
the WebView paints, the strip moves, Explorer's dialog is in front, a main-thread call waits —
WinRAR's held window, which the user named the acceptable price. 7-Zip is on the same path:
its extraction runs inside the outstanding `DoDragDrop`.
And simpler still, the user's next thought: no *Awaiting Windows Explorer* at all — the bar is
ours (the staging), it holds at 100 % while Explorer copies, the only control meanwhile is
Explorer's own window, and when Explorer is done, for whatever reason, the window is released
and the strip destroyed. APP §3 says so; the words are gone.

**No strip, no held window** (2026-09-11, the user's second real drag on the synchronous
build): the drag works, but the strip never appears — not in the hover, not during the
extraction — and the page still takes clicks while Explorer copies. Two things were wrong in
our understanding, and an outside review (Codex Astra, the research file's pass 4) put them
right. First, the page's events travel by `ExecuteScript` through Wails' main-thread
dispatcher, and that dispatcher takes every pending callback in one batch and runs them in
order: when our `DoDragDrop` sits in the batch before the event drainer, the drainer waits for
the drag to end while the "draining" flag keeps later events from scheduling another —
starvation for the whole drag, a Wails mechanism rather than an OLE one (plausible, not yet
measured). Second, and decisive: Explorer's synchronous `Drop` is an *outgoing* COM call on
which our thread blocks, and an STA waiting on an outgoing call dispatches only a handful of
special messages — and the extraction, the very thing the bar is for, runs inside that
`Drop`. With the main thread as the drag thread, the extraction's progress cannot reach the
page by any dispatch; being agile moves our callbacks onto an RPC thread of our own process,
not off the main thread's wait. So the drag must run on a **dedicated OLE thread** — the
review's position, and Chromium's own history (a `Chrome_DragDropThread` with `OleInitialize`,
input forwarded and `AttachThreadInput` for the cursor and the button state) — with the main
thread never waiting on it; the held window then is an explicit `EnableWindow(FALSE)`, and only
from the moment Explorer has the paths (the hover needs the capture, the extraction needs
*Cancel* and the self-drop needs the drop) to `DoDragDrop`'s return. Two things the review
insists be measured before the design is final: the input handoff between the two threads (a
fast release, Escape, Ctrl and Shift, the self-drop), and whether Explorer's cross-volume copy
is really over when `DoDragDrop` returns (the staged file still read after the return would
forbid deleting it then). The prototype gains `-postprobe`, `-thread`, `-disable` and a
cross-volume poll for exactly that; the design paragraph waits for the numbers.

**Measured, and ruled: the drag thread** (2026-09-11, evening; the research file's last
section). On the main thread, posted messages reach the window through the whole hover — so
the strip's absence there was Wails' batch of callbacks, not OLE — and none reach it during
the extraction inside Explorer's `Drop` — that is OLE, an outgoing call's restricted pumping.
On a dedicated OLE thread with `AttachThreadInput` (the drag thread attached to the window's,
Chromium's direction) the drag works and the window's queue is alive throughout, extraction
included. And across volumes the staged file was still being read eighteen seconds after
`DoDragDrop` returned: Explorer's `Drop` hands the copy to its engine and returns. Ruled: the
drag runs on its own thread (`OleInitialize` there, the main thread never waiting on it); the
window is not held — there is no moment to hold it for, Explorer's copy being its own affair
after the return, and Enfold's own extraction already wears the strip with *Cancel*; the strip
is *Extracting N items*, its bar during the staging, full until `DoDragDrop` returns, then
gone; the staging folder is deleted at the return only for a move that took everything, a
self-drop, or nothing written — never by the target's window class — and the scavenge takes
the rest. The main-thread `OleInitialize` at startup, which killed the first launch, is no
longer needed.

**The drag thread in the real window** (2026-09-11, late evening, the user's test with a
real video): the strip appears from the press, the bar moves through the staging, and a fresh
write, a *Skip* and a *Replace* all end cleanly; the window is not held, but Explorer's copy
dialog and the destination folder come to the front by themselves, which the user called
acceptable. Two rulings from what was seen: the bar stalled for seconds at each file's end —
the per-file `fsync` a real extract pays — so the staged copy is written without it (it is
disposable; Explorer's copy is what lands); and *Cancel* vanishing at the second phase reflowed
the strip thinner, so it stays, greyed, and the strip keeps its shape.

**The staging under `%TEMP%`, and a sweep at exit** (2026-09-11, the user's question: does
Windows clean up for us at a reboot?). It does not: `%LOCALAPPDATA%\Enfold\drag` is nobody's
temporary folder; `%TEMP%` is not emptied at boot either; delete-at-reboot needs an
administrator and, under Fast Startup, a shutdown is a hibernation that never processes it.
The only cleaner is Enfold. So: a sweep at a normal exit too (one pass within the shutdown
budget, a folder in use left for the next launch); and the staging moves to `%TEMP%\Enfold\drag`
— WinRAR's neighbourhood — for three reasons the user weighed: Storage Sense and Disk Cleanup
sweep `%TEMP%` eventually (weak, incidental, never relied on, but where users and cleaners look
for an application's leavings); a user's `TEMP` redirection is honoured — a RAM disk keeps the
plaintext off persistent storage for free, another volume only turns a desktop drop from a
rename into a copy; and the later "open a file in an outside program, repack what changed"
workflow belongs in the same place, under the same manifest and sweep. The hour stays.

## 2026-09-11 — The Archives page's bottom bar, the banner's line, the row's focus ring

The user's pass over the pages after the drag-out: the locked banner's text sat below the
middle of its button — one line of 40 px now, the small button, the text centred; the
Archives page's details pane, stacked under the list in a narrower window, was a tall block
that pushed the list away — it becomes a bottom bar: 44 px collapsed with *SELECTED* and the
name and a greyed chevron, the list above it and never under it, the name appearing on a
click without the bar growing, the expand sliding the panel up over the list with the selected
row brought to the first line, the collapse sliding it back, everything animated, the
threshold a fixed 1120 px of body width rather than a proportion, and the wide window's side
pane untouched; and the focus ring on a selected row, drawn by `outline` on the `<tr>`, came
out black, square and uneven (thicker under the name where the row's border met it) — it is
an inset ring of a green a step darker than the selected row's ground, even on every side,
on every selectable row of both lists.
Refined on the first build (2026-09-12): not a bar plus a drawer but **one panel** — its
header is the collapsed line and stays fixed at its top when expanded, one line becoming
two; the list is never resized, only covered, the panel stopping where the list still shows
its header and the selected row, scrolled to the top; the whole header is the control, the
chevron a sign; the body scrolls under the header.
And the motion, as the user pictured it: the rising panel *pushes* the selected row up ahead
of its top edge, the list scrolling in step, at most to the first slot; the collapse is a spring let go — the list scrolls back down while blank space opens beneath
it and rows are still pushed out above, and stops when either runs out. The second build
read the push as bounded by the list's end, which left the last row of a list already at
its end with a panel no taller than its clipped header: the spring sentence only means
something if the push can carry the list past its end, so it does — a spacer under the
panel gives the room, every row reaches the first slot, the panel always opens to the same
height, and the collapse takes the spacer back with the scroll.

**The panel's frame cost, measured** (2026-09-12). The first build of the bottom panel on the
user's 240 Hz monitor: one or two frames badly late at an expand or a collapse, and the body's
scroll choppy; the user also saw the foot's countdown refresh as they scrolled and asked
whether every pixel raised an activity event. Measured in the browser pane at 238 Hz with a
post-render probe (the long-animation-frame API is blind below 16 ms): every glide frame cost
2.0–2.4 ms of main-thread time against 4.17 ms — the card had no compositor layer, so each
frame's height write re-laid-out and repainted the panel, its rounded clip and its shadow, and
re-scrolled a 40-row table painted on the main thread — with three discrete spikes: the
click's own task (4.5 ms, a whole frame), a forced layout in the handler when the spacer was
set (`void scrollHeight` after a padding write through an inherited custom property: the
first painted frame at 11.6–12.8 ms, the panel frozen for three frames), and the spacer's
removal on the collapse's last frame (5.0 ms). The idle countdown and the `vault.state` flush
on an accepted Activity were bystanders (≤ 0.5 ms end to end); Activity is one call per five
seconds while the user scrolls, never per pixel, and the countdown's refresh is the design —
a wheel is input. A/B in the same rig: writing the height to the element's own style instead of the inherited
`--panel-h` was the single biggest win — the median frame 2.3 → 1.7 ms, no dropped frames in
either direction, the over-scroll expand's first paint 11.6–12.8 → 4.7 ms, the collapse's end
spike gone — while a layer on the list's scroller gained nothing (its scroll already cost
0.9 ms a step) and a layer on the card added little once the variable was gone; the body's
scroller halved its step cost with one. Ruled (APP §6): the card's inner is laid out once at
the open height and the card's height alone animates over it; card, inner and the body's
scroller are compositor layers below the threshold; per-frame values go to the element's own style, never an
inherited custom property; the handler reads everything, then writes, with no forced layout;
the spacer is an element after the table and the list's viewport is held while the panel is
up (a padding spacer grew a short list instead of scrolling it — a review's finding); the row
is pushed from contact rather than eased in step; the body is inert on collapse; sort,
filter, resize and the breakpoint reconcile. From the same review (Codex Astra, 13 findings,
all anchors spot-checked): the foot's countdown shows the nearer of the two deadlines; the
file list's focus ring keeps its right edge where columns are dropped; an event's status
never overwrites an operation the page tracks; the idle timer carries a generation and checks
its deadline before locking, since a callback already fired cannot be stopped; the banner's 40 px is a minimum; a remnant in `Activity()` is gone. And a find on the side: the
save bar's frost never rendered in a build — the minifier kept only the `-webkit-` declaration
and the engine honours only the standard one — so the prefix goes.

**The rebuilt panel, reviewed** (2026-09-12). The pass built to the ruling above measured as
intended (the handler's forced layout gone, 0.1-0.4 ms; the first painted frame 5.0-5.5 ms
from 11.6-12.8; the median frame 1.6-2.0 ms; the short list scrolling) and a second outside
review (Codex Astra, eight findings, every one on the panel and every one real; nothing on
the countdown, the status merge or the timer generations) closed the sequences the first
build had not thought through: a selection the filter took out could not open (the card
stayed a line with the open header's shape) - it opens to its height with nothing to push;
a row sorted far down the list while the panel was up could not ride the edge home and the
collapse ended displaced - a ride that cannot end at home eases there instead (APP §6); a
banner or an operation strip appearing above a short, held list moved it without a size
change the observer saw - the split itself is observed; a sort or a second selection during
the opening glide froze the card at its intermediate height - the shift always drives it to
the open height; the resize path read after it wrote - it writes, then measures a frame
later; a filter that removed the row mid-glide left the loop running - it is cancelled; the
breakpoint crossing left a hidden field focused - the header takes the focus; and the
header's cross-fade ran on CSS transitions beside the frame clock - its opacities are the
glide's now, a function of the card's height, so a reversal or a snap cannot leave them
behind.

**Close waits for the lock's handle** (2026-09-12). The test suite's random "TempDir RemoveAll
cleanup: The directory is not empty" had been put down to the antivirus. Half of it was ours:
a lock hands the keystore to a goroutine (`afterLock`, which first waits for a cancelled
ceremony's end and a pending touch, then closes the file), and `Core.Close` returned without
waiting for it, so whoever removed the vault's directory right after Close — every test's
cleanup — could race the open handle. Close now waits for that goroutine — for at most one second, and never past what is left
of §5's three after `ResolveForShutdown` has had its share (closing the handle is
microseconds; the bound is for what afterLock waits for first, the card's own fifteen
seconds, which is not the shutdown's to wait for; an outside review caught the first cut
adding its second outside the three) — before the preview server stops: §5's order and its
~3 s stand, and §2.3's Close, the
archive's, is not this one. Three deterministic tests, one of which reproduces the flake
exactly when the wait is taken out. The other half is the machine's: in ~1 % of removals a
filter driver holds a delete-pending child (an upper-cased `VAULT.EKS.tmp` nothing in this
repository writes) for a few hundred milliseconds, and Go's cleanup retries sharing
violations but not `ERROR_DIR_NOT_EMPTY`; a retry 300 ms later always succeeds. That is the
scanner in the Go temp folder despite its exclusion — a setting, not code — and is left to
the machine.

## 2026-09-13 — Before the phone: what is struck, what is deferred, what is asked

The user's pass over the list of what remains before a phone application, after the panel
landed. **Struck:** BitLocker detection — no unelevated mechanism was measured and the product
never asks for elevation; the import dialog's sentence about BitLocker-protected drives stays
as advice. **Deferred, with its reason written:** code signing — "a personal project,
distributed from GitHub, which is why NSIS"; the certificate's cost is not justified; the
release notes will say the binaries are unsigned. **Kept as it is:** the password's minimum of
8 with no entropy estimate — a word-aware estimate has no standard to agree with, the entangled
password is optional, and a password as the only way in is not the recommended one. **To
Astra, since there is nobody else to look:** the outside review of `FORMAT.md` and of the fuzz
harnesses' reach (SCOPE "Before the format is frozen", items 2 and 3); the feasibility of
`memguard` and `VirtualLock` in a Go program built without cgo; and PDF preview under one
condition — an RCE in the PDF renderer must not reach the keystore, and if that cannot be
excluded the PDF is extracted to a temporary file and handed to the browser. **Wails:** the
version is to be looked at (beta.17–20 exist; a probe in a worktree). **Decided by use:**
destroy, not hide, on the way to the tray — reopening shows nothing worth ~130 MB resident.
**And a rule:** the close button never means "to the tray" by default. The first close — the
button or Alt+F4 — asks whether to keep Enfold in the tray or to quit, with *Remember my
choice*; the answer and the asking are both changeable in Settings; the `closeToTray`
destroy/hide setting goes with it. The tray's *Close all archives* goes too: the window's
close already leaves every page, and an item that changed the core under a page that did not
refresh was worse than none.

**Three evaluations, and what they settled** (2026-09-13, Codex Astra, each alone — its
subagents would not spawn). **`memguard`:** not adopted. In a Go program every secret crosses
into crypto/aes, HKDF, Argon2 and ML-KEM as a plain slice the library copies, so an enclave
protects nothing past the first call, and the threat in scope — the pagefile, a later read of
the disk — is met by the smaller thing: the retained VMK, KWK and K_P in pages allocated
outside the Go heap, `VirtualLock`ed and erased explicitly; best-effort zeroing elsewhere;
Windows Error Reporting off for the process (SCOPE's line rewritten). **PDF preview:** no
route inside the window gives the isolation the user asked for — pdf.js in the page inherits
the page's authority, which is every bound service; a cross-site iframe separates the DOM but
the runtime shim runs in child frames too; a second Wails window shares the application's
bindings and runtime, and a bridge-free WebView2 is integration work past what beta.16
offers; the built-in PDFium viewer is available but its toolbar writes plaintext to disk and
a policy can swap it for another reader. So the user's fallback stands as the design: a
temporary copy under the staging folder, opened by the program the user chooses (a browser,
which is sandboxed, rather than whatever PDF handler ShellExecute finds), with the manifest
and the sweep drag-out already has — and the same lifecycle is what "open a temporary copy,
edit, put it back" will need, so it is designed once (to be written up when it is built; not
in v1). Two things the pass found on the way are fixed now: a page could add the vault's own
file to an archive and read it back (`AddFiles`/`Replace` refuse a source inside the data
folder; APP §3), and a staging manifest marked live is never swept even when the process that
owned it is gone (a dead owner makes it sweepable). **`FORMAT.md`:** not ready to freeze — the
reviewer's words — because it says less than the code in several places and treats an
unauthenticated `seq` as a copy's freshness; the precision items (nonce generation, the
registry's landing extent and its barrier, A/B initialization and ties, the duplicated tags,
length-field boundaries, the entanglement salt's two contradicting sentences, Argon2 lanes vs
threads) are being written into the text, the fuzz harnesses' reach widened; the semantic
ones — `seq` in the index AAD, `seq` across compaction, an intact envelope of an unsupported
version, the erasure claim against R31's retained state, a per-key budget for random-IV GCM —
wait for the user's rulings, being format changes.

**The format, before it freezes: five rulings** (2026-09-13, the user's "do as you suggest" on
the outside review's semantic findings; format changes, made now because no file exists yet
that they would break). **`seq` goes into the AAD** of the archive index and of the keystore
registry: an attacker who can write the file could otherwise keep an older valid index (or
registry), raise the plaintext superblock's `seq` to the expected value and recompute its
unkeyed checksum, and the copy would pass as current — with `seq` authenticated, a promoted
copy fails to open. **`seq` continues across compaction** rather than restarting at 1, so
one `archive_id` never reuses a sequence value for different contents and R36's identity
(`archive_id`, `seq`) holds across a copy's whole life; a compacted file carries the old
file's last `seq` plus one. **An intact envelope of an unsupported `format_version` is
refused**, not treated as absent: R33's recovery is for a torn or corrupt envelope, and an
older reader opening a newer file through it — and repairing the envelope on the way — is
the opposite of fail-closed evolution. **R32's erasure claim is qualified**: a deleted file's
wrapped DEK is gone from the index the winning superblock names, and for one more commit it
is still in the index the losing superblock keeps (R31); the next commit retires that, and a
compaction erases it for good — the text says so. **Random-IV GCM gets its budget written
down**: SP 800-38D §8.3's 2^32 invocations per key for random 96-bit IVs; the wrapping
domains (the KWK, the archive wrap key, the identity and secrets keys) spend a handful of
invocations per commit, so the ceiling is unreachable in a file's life, and the text states
the bound and the assumption instead of a counter. The KDF test vectors and the fuzz seeds
follow the AAD change.

**The hardenings, reviewed** (2026-09-13). The outside review of the vault-source refusal, the
dead-owner sweep and `secmem` found six things, all real, and the fix found a seventh: a
`\\?\` spelling or an 8.3 name of the vault passed the name comparison; the check was on the
name given while the open, later, would follow a link swapped in between — now the opened
handle answers, its identity against the vault file's (`SameFile`, which sees a hard link) and
its final path against the data folder; an extraction checked its root and then followed a
junction beneath it — it never descends into a reparse point now (`file.destination_link`);
the sweep's "an unknown owner is dead after a day" was a guess — an unknown owner is never
swept while the manifest says live, a process that exists but refuses to be queried is alive,
a recorded id without its creation time is unknown; the WER exclusion took a path longer than
`MAX_PATH` — the base name goes then. And the seventh: `filepath.EvalSymlinks` left a
junction as it found it on this machine, so a folder junctioned onto the data folder had
compared as unrelated — the file system's own final name (`GetFinalPathNameByHandle`) is
the canonical spelling now. `secmem` itself drew no finding.

**The five rulings, implemented — and the one thing they needed** (2026-09-13). `seq` in the
AAD collides with the A/B duplication: a writer that must put both copies on one state writes
the losing copy at `seq − 1` (R31's retirement before a truncation; a new file's copy B at 0;
a compacted file's copy B at n − 1), and that copy names an index sealed at `seq + 1`, so a
strict AAD opened nothing for it — twenty-six of R31's tests said so. Ruled with the
implementation: a reader accepts an index or a registry that authenticates under the copy's
own `seq` **or under `seq + 1`**, and nothing else (FORMAT.md §7, §11, R31). It runs one way:
a copy may name a state one commit newer than its number, never older, so an old extent can
never be presented under a higher number than it was sealed with, and the attack the rulings
were for — keep an old index, raise the plaintext `seq`, recompute the unkeyed checksum —
still opens nothing; a copy can only read as behind the registry, the conservative direction.
The alternatives — sealing the index twice in four places, a real change to the crash
protocol; or letting byte-identical copies share a `seq`, which §4 forbids — were rejected.
The version stays 1: FORMAT.md's own Status says nothing is frozen until v1 ships, and
Revision 2 set the precedent. The compacted file's continued sequence reaches the registry
too: `Compact`'s receipt names the source's last `seq` plus one rather than 1, so
`hash_at_seq` is current instead of a file's worth of commits behind. Every writer now refuses
at 2^64 − 1 (`ErrSeqExhausted`, before any byte is written). The KDF vectors were untouched —
they cover the KDF chain and the key-wrap AADs, not the metadata's.

**The tolerance, reviewed twice** (2026-09-13). The second outside pass on the format
accepted the tolerance's argument — a state sealed at k can never authenticate under a number
above k — and found what the first implementation of it left open: a copy opened through the
tolerance kept the copy's number in memory, so the next commit sealed a different state under
the number the previous state already carried, and the replaced file, restored, read as
current against the registry. Ruled: **the state's number is the one the index authenticated
under, and a writer continues from it, never from the copy's** — the open adopts it, the
keystore the same, a compacted file refuses an index that authenticates at anything but its
own number. And with it: the exhaustion refusal moved to where a transaction begins, since a
transaction fills free extents as it goes and an abort cannot unwrite an interior one; the
open's steps written as a numbered recipe (selection and the equal-number rejection first, only
the winner opened, two GCM attempts at most per key, no fall-back to the loser on an
authentication failure); the GCM budget restated as an assumption about a person's workload
with the rotations named as the remedy; a plain copy of the file draws no nonce, since a copy
is not an encryption. **And R32 narrowed:** deletion makes a file *unreachable* — no live
index carries its key — not erased: freed extents keep their bytes until reused, and a holder
of the archive key recovers an old index from them without its nonce (GCM is counter mode; a
predictable first block gives the keystream block and AES⁻¹ of it is the nonce and counter),
so what removes the bytes is a compaction, and the page says "unreachable" where it said
"erased" (APP §3, §6).

**Wails v3 beta.16 → beta.20** (2026-09-13). Probed in a worktree before it was taken: the
four tags between them changed nothing this shell binds — the CLI, the templates and the
NSIS scripts byte-identical, `InvokeSync` and the main-thread dispatcher unchanged (the
platform application is still created inside `Run()`, so the 2026-09-11 lesson stands), the
systray, the window events, `NativeWindow()`, the bindings generator and the injected runtime
the same — and one thing worth having: the `log.Fatal` in `WebResourceRequested` that killed
the process when COM would not set a request's out-pointer under load, bypassing the ordered
shutdown, the lock and the drag sweep, is gone (it logs and drops the one request). What is
new and unproven without the window: a CDP-based request-cancellation mechanism, Windows only,
that defers the first navigation behind an asynchronous setup and runs on `WM_CLOSE` and on
`destroy()` — exactly the destroy-and-recreate, file-drop and drag-thread paths this shell is
unusual in. So the upgrade is four files — `go.mod`, `go.sum`, the frontend's `package.json`
and its lock — with no source change, the regenerated bindings a zero diff and the bundle's
hash unchanged, and it lands on the user's launch test (start, tray, close and reopen, a file
drop, a drag out) rather than on the green gates alone; the commit stands on its own so that
a misbehaviour is one revert away.


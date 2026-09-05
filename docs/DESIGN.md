# Enfold — Design Specification

A compression and archive manager whose distinguishing feature is security and privacy.

Status: **design phase, no code written.** Implementation language is **Go** (§14).
Target platform: **Windows first.** macOS/Linux are a later concern, but the container
format is specified to be platform-independent from day one.

> **The name is load-bearing.** `Enfold` appears inside the HKDF `info` strings in §3.
> Renaming the project from here on is a **format change**, not a cosmetic one.

---

## 1. What this is

A compression + encryption manager. One **keystore file** holds the unlock methods and the keys to
every archive; **archive files** are separate and portable, each holding many files. Unlocking the
keystore is backed by a YubiKey (PIV, ECDH) so that the key-encryption key never exists on the
host disk.

## 2. Threat model

**In scope — this design defends against:**

- Stolen laptop or stolen disk
- The keystore or archive files leaking (cloud backup, misplaced external drive, shared machine)
- An attacker who obtains the keystore *and* extracts the YubiKey private key
  (this is why the entangled password exists — see §4)
- Malware without code execution on the host while the vault is unlocked

**Out of scope — this design does NOT defend against:**

- Malware running as the user *while the vault is unlocked*. It can read plaintext directly.
- Kernel-level compromise / rootkits
- Coerced disclosure of the PIN or password
- Evil-maid modification of the application binary itself

> **On Windows there is no process isolation between programs run by the same user, and none can
> be built in user mode.** Another process of the same user opens ours with `PROCESS_VM_READ` and
> reads our memory. A restrictive DACL on our own process does *not* fix this: the object's owner
> implicitly holds `WRITE_DAC`, so a same-user process rewrites the DACL and proceeds — and
> `SetWindowsHookEx`-style injection never goes through `OpenProcess` at all. Real protection of
> this kind is a kernel driver stripping rights via `ObRegisterCallbacks`, which is what
> anti-malware does and is not available to an ordinary application. An AppContainer is **not**
> an answer either, although it looks like one: measured 2026-09-04, an unelevated process of the
> same user opened every running AppContainer process for `PROCESS_VM_READ` and read its memory,
> and the isolated-storage folders grant the user full control. Microsoft's integrity mechanism
> restricts lower-integrity subjects only — "preventing information disclosure is not a goal" —
> and AppContainer exists to protect the system from the app, not the app from the user's other
> programs. The one Microsoft mechanism aimed at this threat is a VBS enclave (Windows 11
> 26100.2314+, Trusted Signing with enclave EKUs, MSVC-only, so closed to a Go codebase); PPL is
> reserved for anti-malware vendors. A separate SID helps only as a genuinely different account —
> a service process holding the keys, which would change what an attacker gets (use while
> unlocked, not extraction) at the price of administrator rights at install. **Rejected on
> least-privilege grounds: the application never asks for rights it does not need**, and the
> limitation is accepted as Windows' own (`DECISIONS.md` 2026-09-04). The core/UI boundary is
> nevertheless message-shaped, with `KWK` and session state on the core side only.
>
> **Do not build any argument on same-user isolation, and do not add measures that imply it
> exists.** What actually helps is already in the design: secrets in memory rather than on disk
> (which narrows the attack window from *any time later* to *concurrently*), short windows, and
> authority bound to a session rather than to a device.

**Known and accepted — a future cryptographically-relevant quantum computer:**

Every slot passes through an elliptic-curve operation (P-256 for hardware slots, X25519 for the
recovery slot). Shor's algorithm does not halve that security, it **removes it**: a private key is
recoverable from the stored public key. The symmetric layer is unaffected in practice — AES-256
and SHA-256 degrade to ~128-bit effective strength under Grover, which remains out of reach.

The concrete consequence is **harvest-now-decrypt-later**, and archives are a good target for it
because their value does not expire the way a session key's does. An adversary who copies the
keystore today and later gains such a machine recovers `H` from the stored `epk` or
`slot_pubkey`, and then:

| Configuration | Outcome |
| --- | --- |
| Hardware slot **with** entangled password | Falls back to the password through Argon2id — holds, if the password has real entropy |
| Hardware slot **without** password | Opens |
| Recovery slot | **Holds** — hybrid X25519 + ML-KEM-1024 (`FORMAT.md` §3.1) |
| Standalone password slot | **Holds** — same hybrid, over the Argon2id output |

The software slots are hybrid because leaving them classical was worse than it looked: a classical
asymmetric slot hands a quantum attacker the private key straight from the stored public key,
which **bypasses the recovery key and the standalone password entirely** — no grinding at all. For
anyone who had set an entangled password, the recovery slot was a free back door around it.
Hybridising is nearly free (`crypto/mlkem` is in the Go standard library) and closes that.

**The hardware slot cannot be treated the same way**, and that is the accepted limitation: the PQ
half would have to run on the token, and no shipping YubiKey performs ML-KEM. Because slots are
OR-semantic, that one slot sets the ceiling for the whole keystore.

This is deliberately *not* worked around. ML-KEM is asymmetric, so a future PQ hardware slot would
be quantum-resistant **and** keep the offline re-provisioning this design depends on; the
symmetric alternative available today (FIDO2 `hmac-secret`) would buy the first only by destroying
the second, permanently, to route around a hardware gap that is expected to close. Adopting a PQ
PIV applet later costs one new `slot_type` and one new `alg_id` — the format already has room
(`FORMAT.md` §16).

It does give the entangled password a second, independent justification alongside EUCLEAK: it is
the last line of defence under both "the hardware assumption failed" and "elliptic curves failed".
That makes it the right thing to recommend for high-value containers — though still not to enable
by default, since a mandatory high-friction ritual gets defeated by users shortening or removing
the password, which is worse than not offering it.

**Operational guidance follows from this and belongs in the product**, not only in a document:
never put the keystore in public cloud storage; encrypt the storage layer with BitLocker (system
volume, and BitLocker To Go for removable backups), remembering that it is volume-level and so
does nothing for a file uploaded off that volume. `FORMAT.md` §16 has the detail.

**Partially mitigated (best effort only):**

- Hibernation and crash dumps. `VirtualLock` prevents paging to `pagefile.sys` but does
  **not** prevent hibernation from writing all of RAM to `hiberfil.sys`. Suspend
  notifications are not guaranteed to arrive (forced hibernate, power loss, Modern
  Standby / S0ix). See §10 for what we do anyway, and the BitLocker dependency.

## 3. Key hierarchy

```
YubiKey PIV slot 9d -- P-256 private key SK_YK, never leaves hardware
   (Key Management)    PIN policy = ONCE, touch policy = ALWAYS
        |
        | ECDH(SK_YK, epk)      epk = per-slot ephemeral public key, stored in the header
        v
        H  (raw ECDH shared secret, the X coordinate)
        |
        |  entangled password (OPTIONAL, per slot)
        |  pwd'  = HMAC-SHA256(key = H, msg = user_password)
        |  salt' = SHA256(salt || vault_id || recipient_id)
        v
     Argon2id(pwd', salt', m, t, p)     <-- skipped entirely when no password is set;
        |                                   H then feeds HKDF directly
        v
     HKDF-SHA256(info = "Enfold/v1/IK" || vault_id || recipient_id)
        |
        v
       IK  (256-bit slot key)
        |
        | AEAD-decrypt the wrapped VMK   (AAD = the whole slot record, see §5)
        v
      VMK  (Master Vault Key, random 256-bit, memory only, never persisted)
        |
        +-- HKDF -> DB key         (resident while the vault is open)
        +-- HKDF -> Metadata key   (resident while the vault is open; encrypts the registry)
        +-- HKDF -> KWK_identity   (wraps the device identity key -- SYNC.md)
        +-- HKDF -> KWK            (global; unwraps every archive key -- see §10)
                     |
                     v
             archive key (random 256-bit, one per KID, wrapped under KWK, kept in the keystore)
                     |
                     +-- HKDF -> archive index key   (encrypts that archive's file list)
                     +-- HKDF -> archive wrap key
                                    |
                                    v
                           per-file DEK (random 256-bit, one per file, wrapped inside the archive)
                                    |
                                    v
                           content = STREAM(AES-256-GCM, DEK, [zstd(plaintext)])
```

Three key levels, not two. The middle one is what lets an archive be portable but locked: it
carries everything except its own archive key, which exists only in the keystore. `FORMAT.md` §3
gives the exact `info` strings.

### Why the password is folded in with HMAC instead of Argon2's `secret` parameter

Argon2 has a `K` (secret) and `X` (associated data) parameter. Using `K` for `H` would be
the textbook expression of this design. We deliberately do **not** use it:

1. Go's `golang.org/x/crypto/argon2` (verified against v0.55.0) exposes only
   `IDKey(password, salt, time, memory, threads, keyLen)`. There is no `secret` and no `AD`
   parameter. The pure design is inexpressible in Go.
2. More importantly, `K` and `X` are rarely used in practice and therefore thinly covered by
   test vectors across implementations. That is a bad property for a format meant to stay
   readable for years.

The HMAC fold gives the identical security property: without `H` an attacker cannot compute
`pwd'`, so cannot begin the Argon2 grind at all. That is the whole point of the entanglement
— an attacker holding only the keystore file has nothing to work on.

### Why P-256 and not X25519

X25519 (YubiKey firmware 5.7+) avoids a class of point-validation footguns and would
otherwise be the better choice. We use **P-256** anyway, because iOS Secure Enclave stores
only 256-bit EC keys and performs P-256 ECDH. Choosing X25519 would permanently close the
door on the phone-as-hardware-key direction (§12). This is a deliberate trade.

## 4. Why an entangled password at all

The YubiKey is the primary factor, but hardware guarantees are not permanent. YSA-2024-03
(EUCLEAK) revealed a side-channel in the Infineon library affecting YubiKey 5 firmware
< 5.7, with **no possibility of a firmware patch**. That advisory covers ECDSA and does not
mention ECDH, so this design is probably not directly affected — but the lesson generalises.

The entangled password means that even a fully extracted `SK_YK` does not open the vault. It
converts "the hardware is trustworthy" from an assumption into one of two required factors.

**However, the password is only as good as its entropy, and a KDF cannot fix that:**

- Argon2id at m=1 GiB buys a roughly fixed **2^25** slowdown versus an unhardened hash.
- A 1,000-GPU adversary reaches roughly **2^42** guesses per year against these parameters.
- Human-chosen passwords satisfying classic composition rules land around **25–40 bits** —
  inside that reach, in hours.
- A 6-word EFF diceware passphrase is **77.6 bits**. Permanently out of reach.

KDF hardening is additive; entropy is the base of the exponent. Therefore:

- **No character-class composition rules.** NIST SP 800-63B-4 states these SHALL NOT be
  imposed: they produce predictable patterns and reject strong passphrases.
- **Estimate real guessing entropy** (zxcvbn or equivalent) and show the user the concrete
  consequence — "~38 bits, roughly 2 hours against a 1000-GPU attacker" — not a coloured bar.
- **Ship a diceware generator**, offered but never mandatory. Users may choose convenience;
  they may not choose it uninformed.

## 5. Key slots

The VMK is wrapped independently once per unlock method. Each slot record stores:

| Field | Notes |
| --- | --- |
| `slot_type` | yubikey / standalone-password / recovery-key |
| `recipient_id` | stable identifier, used in the HKDF info and the AAD |
| `epk` | ephemeral public key (YubiKey slots only) |
| `salt` | Argon2 salt |
| `argon2_params` | m, t, p — **must be authenticated, see below** |
| `has_entangled_password` | bool |
| `wrapped_vmk` | AEAD ciphertext + tag |

**The entire slot record is the AAD of `wrapped_vmk`.** Argon2 parameters must be read
*before* anything can be decrypted, so they cannot be encrypted — but they must be
authenticated, or an attacker downgrades `m` from 1 GiB to 8 KiB and brute-forces cheaply.
The same applies to `epk` and `salt`.

### Two different things are called "password"

These must never be conflated in code. Give them distinct types.

| | Semantics | Opens the vault alone? |
| --- | --- | --- |
| **Entangled password** | part of a hardware slot, folded into the KDF alongside `H` | No — YubiKey required |
| **Standalone password slot** | its own slot, OR semantics | Yes |

Rule: **a standalone password slot may not coexist with any hardware slot.** Otherwise OR
semantics drag the vault's security ceiling back down to password strength.

When a user upgrades from `password + recovery key` to a YubiKey, do **not** silently delete
the password slot. Offer to carry the same passphrase over as the new hardware slot's
*entangled* password: same words for the user, OR becomes AND, nothing is silently destroyed.

### The slot invariant

Counting slots is not sufficient. Two YubiKey slots sharing one entangled password are two
slots, but forgetting that password kills both.

> **Invariant: there must exist two slots whose required-secret sets are disjoint.**

This is a predicate evaluated before *every* slot add / remove / replace, not a check written
once in the creation flow.

| Configuration | Required-secret sets | Valid |
| --- | --- | --- |
| Two YubiKeys, no password | `{YK_A}` / `{YK_B}` | yes |
| Two YubiKeys, shared entangled password | `{YK_A,pwd}` / `{YK_B,pwd}` | **no** — needs a recovery key |
| One YubiKey + password + recovery key | `{YK,pwd}` / `{rec}` | yes |
| Standalone password + recovery key | `{pwd}` / `{rec}` | yes (low-security tier) |

### Recovery key

Randomly generated, **128 bits**, never user-chosen, not entangled with the password.
Because it is full entropy it needs no Argon2id — HKDF directly is sufficient.

128 bits is already unreachable by brute force. Doubling to 256 bits buys nothing against a
classical attacker while doubling transcription errors on the one artefact a human must copy
by hand. **The binding constraint on a recovery key is how it is stored, not its length.**

**Encoding: the BitLocker format, verbatim.** 48 digits in 8 groups of 6. Each group is
divisible by 11 and less than 720,896 (2^16 x 11), carrying 16 bits — 128 bits in total. The
sixth digit of each group is a check digit: `x6 = (x1 - x2 + x3 - x4 + x5) mod 11`.

The checksum is the point of copying this format. A mistyped group is caught at the moment it
is typed, instead of surfacing as an opaque "wrong key" after the user has entered all 48
digits with no idea which group is wrong.

#### The recovery slot is asymmetric too

The recovery key is therefore a **seed for a keypair**, not a KEK used directly.

The naive construction `IK = HKDF(recovery_key)` is symmetric, and that breaks VMK rotation
(see "Revocation" below). Rotation must re-wrap the new VMK to every surviving slot, and with
a symmetric recovery slot that means **the user has to dig the paper out of the drawer every
time a YubiKey is removed or a password is changed.**

That friction is not a UX annoyance, it is a security failure mechanism: users who find
rotation painful will skip it and delete the slot record instead, which revokes nothing.

```
Creation:  seed            = HKDF(R, info = "Enfold/v1/recovery-keypair")
           (sk_rec,pk_rec) = X25519 keypair derived from seed
           store pk_rec; destroy sk_rec and seed immediately

Wrapping:  identical to a YubiKey slot -- fresh epk, ECDH(esk, pk_rec)
           no recovery key needs to be present

Recovery:  user types the 48 digits -> re-derive sk_rec -> ECDH(sk_rec, epk) -> IK
```

**X25519 here, not P-256.** An X25519 private key is any 32 bytes after clamping, so
deterministic derivation from a seed is trivial. P-256 requires rejection sampling into
`[1, n-1]`, which is fiddly and easy to get subtly wrong. The recovery slot never touches
Secure Enclave, so the constraint that forced P-256 in §3 does not apply here. Slot records
already carry a `slot_type` and an algorithm ID, so mixing curves across slot types is free.

Result: **every slot type is asymmetric, and VMK rotation never requires any physical
credential to be present** — only the entangled password, which the user just typed to unlock.
Rotation can therefore be unconditional and invisible, which is the only way it will actually
happen.

### Revocation

Removing a slot record **revokes nothing on its own.** Anyone who copied that slot record
before the removal can still derive its IK and unwrap the VMK — and since the VMK is
unchanged, so is the KWK, so they can decrypt every DEK including those of files added *after*
the removal.

**Real revocation requires rotating the VMK.** This is what the `IK -> VMK` indirection is
actually for: it does not provide isolation by itself, it makes rotation *cheap*. Rotating the
VMK re-wraps every **archive key** — roughly 60 bytes per archive, not per file, so a vault with a
thousand archives rotates in well under a second — and **no file data is re-encrypted**.

| Depth | Cost | What it actually revokes |
| --- | --- | --- |
| Delete the slot record only | ~1 KB | Essentially nothing |
| **+ rotate the VMK** | ~60 B per archive | **All future access.** The removed credential now yields only the old VMK, which opens nothing in the current keystore |
| + rotate archive keys | index rewrite per archive | Withdraws access to archives whose keys were released to a low-trust machine |

**Rotation is offered on every slot removal and credential change, pre-selected.**

Not silently automatic, and not opt-in either. The reasoning for landing on that default:

- Rotation's entire value is protecting archives created *after* the removal, in the case where
  the old VMK must be assumed to have leaked. Removing a credential is not itself a leak —
  retiring an old YubiKey is routine.
- But **old copies of the keystore are likely to exist** — exports, a copy on a second drive, a
  file that once sat in a synced folder — so "no copy is out there" is an assumption rather than a
  fact. Worse, without rotation an attacker with write access to any copy can splice an old slot
  region back and revive a deleted slot outright (`FORMAT.md` §6.2). Un-rotated removal is
  therefore weaker than it looks, and it fails silently when it fails.
- Yet forcing it every time is friction that users route around by simply never removing stale
  slots — which leaves the credential live, the outcome the ceremony existed to prevent.

**Ask the factual question, not the technical one.** "Also rotate the VMK?" gets skipped because
nobody can answer it. This can be answered:

> **Why are you removing this key?**
> — I still have it and it was never out of my control → rotation not needed
> — Lost, stolen, or I am not sure → rotation strongly recommended *(default)*

**Changing a standalone or entangled password needs its own wording**, since the question above
does not fit it. Without rotation, "I changed my password because I think it leaked" accomplishes
nothing: the old password plus an old keystore copy still opens archives added afterwards. Default
to rotating on any password change.

**A deferred rotation must stay visible** — `rotation_pending` in the superblock plus a persistent
notice — because "later" otherwise becomes "never" (`FORMAT.md` §8).

**This does not depend on TRIM or secure erasure.** After rotation, a slot record recovered
from unallocated SSD blocks decrypts to the *old* VMK, which is useless against the current
keystore. Wear levelling and TRIM only become relevant at the third tier.

**What no design can revoke:** data the adversary could already decrypt at the moment they
took their copy. They had the plaintext available then; nothing done afterwards un-discloses
it. Revocation protects the future, not the past. Say this plainly in the UI rather than
implying that removing a key undoes past exposure.

## 6. Argon2id parameters

- **Memory is the lever, not time.** RFC 9106's first recommendation is `t=1, m=2 GiB, p=4`.
  Prefer raising `m`; keep `t` clamped to 1–4.
- **`p` is part of the algorithm** — changing it changes the output. Fix `p=4` and store it.
  The number of OS threads used to compute may be lower without affecting the result, so a
  dual-core machine still decrypts correctly, only slower.
- **Do not auto-calibrate `t` to the creating machine.** A fast desktop picks `t=6` and a slow
  laptop then takes six seconds.
- **Size against physical RAM, not instantaneous free RAM.** Free memory fluctuates; a vault
  created with 12 GB free must still open when only 3 GB is free.
- Defaults: 512 MB baseline, 1 GB recommended, never more than half of physical RAM.
- Warn when: below 512 MB, above 1 GB (cross-device pain), or above half of physical RAM.
  Refuse anything above physical RAM.
- Check available memory at **both** creation and unlock.

## 7. Two files, three key levels

Byte-level layouts are in `FORMAT.md`; what matters here is the shape and why it is that shape.

```
Keystore file      one per device, in the user's app data directory
   slots  +  encrypted registry of archives and their keys

Archive files      many, anywhere — including places you do not control
   envelope (plaintext archive_id + KID)  +  encrypted file index  +  data
```

```
VMK → KWK → archive key (one per KID) → per-file DEK → file data
```

**Nothing that could unlock an archive is ever written into it.** Encrypted archives are *designed*
to be stored in relatively untrusted places — public cloud, removable media, someone else's
machine — so putting unlock material inside one dismantles the threat model, and would make a
centralised key manager pointless besides.

An archive therefore carries `archive_id` and `KID` in plaintext (so the right key can be found
without trial decryption), per-file DEKs wrapped under the archive key, an encrypted file index,
and ciphertext. **No asymmetric key material**, which is why an archive on its own presents no
target for Shor (§2).

The middle key level is what makes this work: an archive is fully self-describing but locked,
holding everything except the one archive key, which exists only in the keystore.

The file index is encrypted because **filenames leak more than contents do**. A name like
`2026-divorce-agreement.pdf` is the whole story on its own.

**Accepted cost:** separate files leak archive count, sizes and on-disk names, and a plaintext
`archive_id` makes versions of one archive linkable. The alternative — trial decryption against
every known key on every open — is worse.

**Consequence that must reach the user:** without the keystore, every archive is permanently
unopenable however intact the files are, and **the recovery key does not cover this** — it unlocks
*a* keystore, it does not reconstruct one. Keystore export is therefore its own feature, deferred
in design but not in importance; v1 ships a manual export carrying the registry plus **only** the
recovery slot, which is restorable, post-quantum safe, and not useful to an attacker who splices
it (`FORMAT.md` §15). The practical guarantee in the meantime is the slot invariant: at least two
independent ways in.

## 8. File encryption

**One DEK per file, not per archive.** The reason is in-place editing, which is a routine
operation for this product rather than an edge case.

Editing a file inside an archive changes its compressed size, shifting every subsequent byte.
Those chunks cannot be re-encrypted under the same `(DEK, counter)` — that is nonce reuse,
catastrophic for GCM (it leaks the authentication subkey and enables forgery).

| | Editing one 10 KB file |
| --- | --- |
| One DEK per 50 GB archive | rewrite **50 GB** |
| One DEK per file | rewrite **10 KB** |

Secondary benefits: the blast radius of a leaked DEK is one file, a single file can be
exported to someone else, and per-file compression decisions need per-file metadata anyway.

Accepted cost: no solid compression across files, and one index record (~60 bytes) per file.
100k files is ~6 MB of index, which is fine; a million would need rethinking.

### Chunked AEAD (STREAM)

- **AES-256-GCM**, 64 KiB chunks. Windows x86-64 always has AES-NI, making this several times
  faster than ChaCha20-Poly1305 on multi-GB archives.
- Nonce = 11-byte **little-endian** counter + 1-byte final-chunk flag (`0x00` / `0x01`),
  following the STREAM construction used by age and Tink but with this project's one byte order
  (FORMAT.md §1; age's is big-endian). The counter prevents reordering; the final flag prevents
  truncation. Reaching EOF without decrypting a final chunk is an error, and the reader learns
  which chunk is final from the authenticated `stored_size`, never by trial decryption (R26).
- **Do not invent a construction here.**
- **Do not use CBC or bare CTR.** CTR gives random access with zero authentication, meaning an
  attacker can flip bits in a stored file. CBC additionally brings padding oracles and
  malleability. Neither buys anything chunked AEAD does not already provide. (WinRAR's ability
  to add files to an archive comes from its per-file block layout, not from its use of CBC.)
- Chunk-size trade-off: the 16-byte tag costs 0.024% at 64 KiB and 0.39% at 4 KiB, and a seek
  wastes at most one chunk. 64 KiB is the sweet spot; below 16 KiB there is nothing to gain.
- Store both lengths in the authenticated index: `stored_size` fixes the STREAM framing, and so
  which chunk is final — for compressed files too, where `orig_size` says nothing about the
  chunk count; `orig_size` sizes the plaintext the caller sees.
- **Any modification to an existing file mints a fresh DEK.** This is the rule that makes nonce
  reuse structurally impossible rather than merely avoided by care.
- Put an **algorithm ID in the header** so ChaCha20-Poly1305 can be added later without a
  format break (it is the better choice on ARM, which matters if §12 happens).

### Random access and streaming

Chunked AEAD already supports random access: to read `[a,b)` decrypt chunks
`floor(a/C) .. floor(b/C)`. No cipher change is needed for streaming playback.

The real obstacle is compression, not encryption — zstd's sliding window means a single stream
must be decompressed from byte zero. This is moot in v1 given the compression policy in §9.

To hand a stream to an external player without a temp file: a **loopback HTTP server on
127.0.0.1** with Range support, a random port, an unguessable path, torn down when the player
exits. Range requests map directly onto chunks, so scrubbing only decrypts what is watched.
Note that any local process can reach 127.0.0.1 — the unguessable path is the actual access
control, not decoration.

**Known limitation, same family as trap #6:** streaming avoids *our* temp file, but not the
player's. Media players keep recently-opened lists, generate thumbnail caches, and buffer to their
own disk cache. The URL becoming useless once the server stops is not the problem — **cached
decoded content is plaintext on disk**, written by a process outside our control. Say so rather
than implying that streaming keeps plaintext off the disk entirely; it keeps *our* plaintext off
the disk, which is a narrower claim.

## 9. Compression policy

- **Do not compress incompressible data.** Video, audio, images and existing archives gain
  0–2% from zstd and lose clean random access in exchange.
- **Detect by sampling, not by extension.** Compress a 64–256 KiB sample at level 1
  (microseconds); if the ratio exceeds ~0.95, store raw. An extension allowlist is a fast path,
  not the decision — `.bin` and `.dat` could be anything, and PDFs vary wildly.
- **Sample several offsets, not just the head.** A PDF with embedded images has a compressible
  header and an incompressible body; judging from the first 256 KiB alone gets it backwards.
  Three samples — start, middle, and a point two thirds in — cost microseconds and are far more
  representative.
- Consequence: **v1 does not need the zstd seekable format at all.** The files worth seeking
  into (video, audio) are exactly the files stored uncompressed. Seekable zstd can be added
  later behind the algorithm ID, when someone genuinely wants to seek inside a 20 GB
  compressed log.
- For many small compressible files, zstd **dictionary training** recovers most of what solid
  compression would have given while keeping per-file independence. The dictionary is stored as
  encrypted metadata in the container.

## 10. Session model

The product is a *manager*: demanding YubiKey + PIN + touch + password for every archive opened
would be unusable. The global KWK is therefore cached after unlock.

**This is a conscious trade.** A cached KWK unwraps every archive key, so the cache window exposes
the whole vault, not merely the archive in use. Per-archive KWK derivation
(`HKDF(VMK, "wrap" || archive_id)`) was considered and rejected for exactly the UX reason
above. It remains available if the trade is ever revisited.

**Cached:** KWK, DB key, Metadata key.
**Destroyed immediately after use:** VMK.

Destroying the VMK while keeping the KWK is not cosmetic. An attacker who dumps memory holding
only the KWK gets a one-time snapshot of the contents. An attacker who gets the VMK can **add
their own YubiKey as a legitimate slot** and retain silent, permanent access. Least privilege
applies to key caching.

### Timeouts

Both are required:

- **Idle timeout, ~10 minutes.** This is the one that matters. The common real exposure is
  unlocking and then walking away, which an absolute cap does nothing about.
- **Absolute cap, ~1 hour**, regardless of activity.

### Lock triggers

In rough order of how often they actually fire:

1. **Workstation lock / session switch** (`WTSRegisterSessionNotification` →
   `WM_WTSSESSION_CHANGE`). Most frequent and most reliable. Do not omit this one.
2. Manual lock from the UI
3. Idle timeout, absolute timeout
4. Sleep / hibernate / S0 standby (`WM_POWERBROADCAST`) — best effort, see §2
5. Application exit

On vault close, also destroy the decrypted index and the Metadata key.

### An already-open archive when auto-lock fires

Auto-lock destroys the KWK and the VMK and blocks opening anything new. Archives already open
stay usable until closed, so a running stream or an in-progress edit is not killed mid-flight.
Open archives carry their **own idle timeout**; otherwise "keep it open" becomes a way to
bypass the session timeout entirely.

### Making the unlocked state visible

- A banner with a countdown while the vault is unlocked, with a one-click **Lock now**.
- **The tray icon must change** when unlocked. When the window is minimised the banner is
  invisible, and a reminder nobody can see is not a reminder.

### The token ceremony, in the order the user experiences it

PIN first, then touch: the YubiKey asks for a touch only after the PIN has been accepted, and the
person who just typed the PIN is then waiting with no cue (observed 2026-09-04 — "left standing"
after the PIN). The moment the PIN is accepted the UI must switch to an unmistakable "now touch
the key" prompt. Before asking for the PIN it must show the retries remaining: the counter is 3,
and a blocked PUK means a PIV application reset that destroys every slot, including keys the user
keeps on the same token for other purposes.

### The WebView boundary

The UI is a WebView2 (Wails v3, §14). **Keys never cross into the WebView.** Anything handed to
the renderer lives in a JS heap that cannot be zeroed. Send the index one screenful at a time
rather than handing over the whole decrypted file list. The WebView2 also has a disk cache of
its own — trap #13 in §11: nothing decrypted is served to it without `no-store`. Destroying the
window on close-to-tray (provisional, §14) additionally discards the renderer's copy of whatever
was on screen.

### Dependency on full-disk encryption

Because hibernation defeats `VirtualLock` unconditionally, the vault's at-rest guarantee partly
rests on the system volume being encrypted. The application should **detect whether the system
volume is BitLocker-protected and warn if it is not.** The same reasoning applies to crash
dumps — investigate excluding sensitive regions from Windows Error Reporting.

## 11. Traps checklist

Correct in this document, and easy to lose during implementation.

1. **Destroy the ephemeral private key `esk`** after creating a slot, and never persist it.
   `ECDH(esk, PK_YK) == ECDH(SK_YK, epk) == H`; leaking `esk` removes the hardware entirely.
2. **Validate `epk` before sending it to the YubiKey.** It comes from the keystore slot record, so a
   malicious file controls it. Yubico's documentation says the key *ideally* checks that the
   point is on the curve — that is not a guarantee. Go's `crypto/ecdh` validates in
   `NewPublicKey`; do not hand-roll with the raw `elliptic` package.
3. **Argon2 parameters, `epk` and `salt` all go in the AEAD's AAD**, or downgrade attacks work.
4. **Rewriting a file mints a new DEK.** Never reuse a `(DEK, counter)` pair.
5. **Superblock writes are atomic**, or a crash bricks the container.
6. **Plaintext must not reach a temp file** during preview. For executables, where streaming is
   impossible, tell the user plainly that a plaintext copy is being written — and note that
   secure deletion on an SSD is unreliable (wear levelling, TRIM). Prefer a temp location on an
   encrypted volume.
7. **`VirtualLock` does not protect against hibernation.** It locks pages into the working set,
   which bounds `pagefile.sys` exposure only.
8. **Compression before encryption leaks length information** (CRIME/BREACH class). Acceptable
   for an archive manager, but make it a documented per-archive choice.
9. **The slot invariant is a predicate**, evaluated on every mutation — not a one-time check.
10. **Reserve `pack_id` and a per-archive `policy` field in the index now.** Small-file packing
    and "this archive always requires full authentication" are both v2 features that cannot be
    retrofitted into a format with no room for them.
11. **Removing a slot revokes nothing until the VMK is rotated.** Anyone holding an earlier copy
    of the slot record still reaches the unchanged VMK and therefore every archive key, including
    those of archives created afterwards — and with write access they can splice the old slot
    region back outright, and such copies are likely to exist. Rotation is pre-selected on every
    removal and every password change; a deferred one must stay visible (§5, Revocation).
12. **The keystore must never go to public cloud storage**, and the storage layer should be
    BitLocker-encrypted. This is the only mitigation for the post-quantum exposure in §2, and
    BitLocker is volume-level — it does nothing for a file uploaded off that volume
    (`FORMAT.md` §16).
13. **The WebView2 disk cache.** The browser engine caches what it fetches in its user-data
    folder. Every loopback response that carries decrypted content — the media-preview stream
    above all — must be `Cache-Control: no-store`, and the WebView2 profile runs with caching
    disabled or in-private where the framework exposes it. Verify by inspecting the profile
    directory after a preview. Otherwise decrypted content is written to disk by a component
    that is not this program.
14. **The token's PIN-once state outlives our connection.** Measured 2026-09-04 on a YubiKey
    5.7.4 (`DECISIONS.md`): after a VERIFY the card stays verified until Windows powers it down,
    which happens **10 s after the last application disconnects** — piv-go disconnects with
    `SCARD_LEAVE_CARD` and never resets. In that window any process on the machine can use the
    9d key without the PIN; touch is the only barrier. Whatever PIN policy is finally chosen
    (open, `SCOPE.md`), the unlock path must not leave a verified card behind it: disconnect with
    `SCARD_RESET_CARD` through winscard directly (verified to clear the state at once), or hold
    the exclusive connection for the whole session, which locks every other process out.
15. **Rotation re-wraps into stored public keys — verify the slot region first.** §8 step 4
    re-wraps the new VMK to each slot's stored `slot_pubkey` / `mlkem_ek` with no credential
    present. The slot region is only checksummed, so a substituted public key would receive the
    new VMK. The registry carries an authenticated SHA-256 of the slot region (`FORMAT.md` R25);
    verify it after every unlock and refuse to rotate, re-wrap or mutate slots while it
    mismatches. Found by the `internal/kdf` review on 2026-09-05, one layer before the code that
    would have had the bug.
16. **Cap the zstd decoder from the index, not from the frame.** An archive is untrusted input;
    a zstd frame declares its own window size and its own content size, and a hostile one can
    demand a 512 MiB window or expand without bound. Decode with `WithDecoderMaxWindow` set to
    the largest window this program ever writes and `WithDecoderMaxMemory` set to the file
    record's `orig_size`, and treat either limit being hit as corruption. The same reasoning as
    R24: the parameters are read before anything proves them honest.

17. **An error from a STREAM reader means discard, not keep.** Chunks authenticate one at a
    time, so a sequential reader can hand out 300 KiB of authentic plaintext and then fail on
    the chunk after it. Anything that materialises a file — extraction, preview spooling, an
    export — builds into a temporary and renames it into place only after the reader returned a
    clean end (`io.EOF`), and never presents the prefix as the file. The reader's side of the
    bargain is that it never reports a clean end before the final chunk has authenticated (R26),
    so "read to EOF without error" is the whole acceptance test.

## 12. Deferred

- **Small-file packs.** Bundle files under ~64 KB into ~4 MB packs, one DEK per pack, solid
  compression inside the pack. Editing a small file rewrites its pack, not the archive.
  Requires the reserved `pack_id`.
- **Virtual filesystem (WinFsp / Dokan).** The only approach satisfying both "any program can
  open the file" and "plaintext never touches the disk". This is the real answer for
  executables. Significant work; requires a kernel-mode driver to be installed.
- **Phone companion.** Specified in `SYNC.md`. Note the architecture, because an earlier draft of
  this document had it backwards: the phone is **not a slot on a PC's keystore**. The phone
  carries its **own** keystore, with its own VMK and its own slots, and either syncs metadata and
  DEKs with a PC that also has its own keystore, or — on a low-trust machine — releases the keys
  for a single archive over an end-to-end encrypted channel while the VMK and KWK never leave the
  phone.
  - The Secure Enclave / StrongBox P-256 key is how the **phone unlocks its own keystore**. That
    is `key_source = phone-native` in `FORMAT.md` §9 — the same external-ECDH slot shape as a
    YubiKey, which is why no new slot type is needed.
  - Prefer **Key Attestation** (Android certificate chain, iOS App Attest) over Play Integrity.
    Attestation proves the property actually wanted — that the key was generated inside
    StrongBox/SE and is non-exportable — and is verifiable client-side. Play Integrity is a
    server-side device check that excludes rooted devices and custom ROMs, plausibly this
    product's users.
  - **Android needs a three-tier fallback, and each tier must be disclosed.** StrongBox ECDH is
    *not* a given: `KeyAgreement` support arrived with StrongBox version 100 / API 31, and
    StrongBox implements only a subset of algorithms, varying by vendor, with key generation
    permitted to fail outright when the hardware lacks support. So: **StrongBox → TEE-backed
    Keystore → pure software**, and the UI states which tier is in use at enrolment and at every
    unlock. The software tier is labelled plainly as basic integrity and relatively weak
    protection. Silently degrading and continuing to imply hardware backing would be the worst of
    the three outcomes.
  - Treat `phone-native` on Android as **unverified until tested on real hardware**. It should not
    be scoped into a release on the assumption that it works.
- **Passkey-backed slot (`key_source = prf-derived`).** The ID is reserved but nothing is planned.
  A WebAuthn PRF output can be turned into an asymmetric slot by deriving a P-256 keypair from it
  and storing the public half — the same trick as the recovery slot. It would fit a future
  "unlock with Windows Hello" feature. It is **not** the phone-companion design and should not be
  confused with it. Two findings to carry forward if it is ever picked up: PRF output stability
  across synced/restored passkeys is an undocumented implementation behaviour rather than a
  guarantee, with observed divergence between Apple devices sharing one iCloud credential; and
  the WebAuthn Level 3 co-editor published a case against using passkeys for data encryption at
  all, on the grounds that delete UIs give no warning that removing a credential destroys
  ciphertext. Any such slot must never be a container's only slot.
- **Per-archive authentication policy.** Mark a sensitive archive "always require full
  authentication, ignore the session cache". Localises the UX/security tension instead of
  compromising globally. Requires the reserved `policy` field.
- **Seekable zstd**, if seeking inside large compressed files ever matters (§9).

## 13. Document map and remaining gaps

| Document | Covers |
| --- | --- |
| `DESIGN.md` (this one) | Threat model, key hierarchy, slots and the invariant, Argon2 policy, chunked AEAD, compression, session model, traps |
| `FORMAT.md` | Byte-level layouts for both file types, record encodings, AAD definitions, algorithm ID registry, VMK rotation procedure, free-space map |
| `SYNC.md` | Device classes, identity keys, handshake, merge rules, low-trust-PC mode, relay exposure, abuse cases |
| **`SCOPE.md`** | **What v1 actually ships, what it explicitly does not, and what must happen before the format is frozen** |
| `DECISIONS.md` | Dated log of every decision with its reasoning and what was rejected |

**Read `SCOPE.md` before implementing anything here.** These documents describe a system
substantially larger than v1; most of `SYNC.md` in particular is out of scope for the first
release.

Remaining gaps are listed at the end of `FORMAT.md` and `SYNC.md`. Nothing in the cryptographic
design is open.

Settled by implication, recorded here so it is not re-litigated: the entangled password is
**vault-wide**, shared across all hardware slots, rather than per-slot. The slot invariant
(§5) already handles the consequence — a shared password makes two YubiKey slots
non-disjoint, so that configuration requires a recovery key.

## 14. Language and toolchain

**Decision: Go**, with **Wails v3** for the UI — a web UI rendered in the system WebView2.
v3 rather than v2 because only v3 can run as a zero-window tray process and destroy its window
on close; v2 can only hide it, which keeps the whole WebView2 process group resident. v3 has
been in beta since 2026-08-02; pin a specific beta tag, do not track nightly. The measurements
and the reasoning, including why a web UI is accepted against the original objection to one, are
in `DECISIONS.md` (2026-09-04). Fallback if the trade ever turns bad: `github.com/tailscale/walk`,
native Win32 controls at ~4 MB, which the Go core would not notice.

**Memory budget, measured 2026-09-04 (private working set, whole process tree):** idle in the
tray with the window closed, one Go process, 7–9 MB. Window open, ~130 MB baseline from the
WebView2 process group before any content — that group is an architectural floor of Chromium,
not a setting. **Provisional default: closing to the tray destroys the window** rather than
hiding it, which is what makes the idle figure real; reopening recreates it (0.2 s measured warm
with the stock page — the real interface must be measured). **Pending, to be settled once the
real UI exists**, on one criterion: if reopening shows no noticeable delay, stutter or state
loss, destroy stays; if it does, the window is hidden instead and ~130 MB resident is accepted
as the price (SCOPE.md, "Deliberately unresolved"). The session cache lives in the Go process
either way and is unaffected.

The whole dependency stack was verified to build and run with `CGO_ENABLED=0` on
windows/amd64, so **no C compiler is required**. Wails v3 carries its own pure-Go WebView2
binding in-tree (`v3/internal/webview2`); the probe binary's build info confirms
`CGO_ENABLED=0`. Versions as resolved on 2026-08-31 (Wails on 2026-09-04):

| Dependency | Version | Role |
| --- | --- | --- |
| `github.com/go-piv/piv-go/v2` | v2.6.0 | YubiKey PIV; pure Go, talks to winscard directly. Verified on hardware 2026-09-04 (GET METADATA, AES management keys, ECDH). Lacks MOVE/DELETE KEY (0xF6) and never resets the card on disconnect — trap #14 |
| `github.com/awnumar/memguard` | v0.23.0 (+ `memcall` v0.4.0) | secrets outside the GC heap |
| `github.com/klauspost/compress` | v1.19.2 | zstd, pure Go |
| `golang.org/x/crypto` | v0.55.0 | argon2 (`IDKey` only — see §3) |
| `crypto/ecdh` | stdlib | P-256 for YubiKey slots, X25519 for the recovery slot; validates points on `NewPublicKey` (trap #2) |
| `crypto/hkdf`, `crypto/aes`, `crypto/cipher` | stdlib | HKDF, AES-256-GCM |
| `github.com/wailsapp/wails/v3` | v3.0.0-beta.16 (pin) | UI: zero-window tray app, WebView2 window on demand, file drop, tray icon |

Runtime requirement: the **WebView2 Evergreen runtime** must be present. It is part of Windows
11; on Windows 10 it was rolled out by Microsoft in 2022–2023 and is installed by Microsoft 365
Apps, but a small number of machines still lack it. The installer checks the runtime's `pv`
registry value and runs Microsoft's ~2 MB bootstrapper when it is absent. Microsoft has committed
to updating Edge and WebView2 on Windows 10 22H2 until at least October 2028.

Installed on the development machine: the `wails3` CLI (v3.0.0-beta.16), Node 24 and npm 11 for
the frontend build.

### Why Go, given the memory-hygiene argument

The gap between Go and Rust here is **not a capability gap**, and it is narrower than the
usual framing suggests.

**Go can keep secrets out of the GC entirely.** [`memguard`](https://github.com/awnumar/memguard)
allocates through direct syscalls, bypassing the Go runtime, so the collector never scans,
copies or touches the region. It applies `VirtualLock`/`mlock`, adds guard pages and canaries,
and — notably — **keeps the secret encrypted in memory (XSalsa20Poly1305), decrypting into a
locked buffer only for the moment of use.**

That last property is worth dwelling on: `zeroize` only guarantees erasure *on drop*. While a
key is legitimately alive, plain Rust leaves it in cleartext in memory — and hibernation can
land precisely in that window. On this specific axis, **Go with memguard is ahead of a default
Rust + `zeroize` setup.**

Go's collector is also **non-moving**, including the Green Tea GC that becomes the default in
Go 1.26, so explicit zeroing genuinely works. (Non-moving is a long-standing implementation
property, not a language-spec guarantee — but it has held for a decade and survived the Green
Tea rewrite.)

**What Rust actually buys is that the compiler catches the mistakes:**

```rust
let key: Secret<[u8;32]> = ...;
println!("{:?}", key);   // Secret has no Debug  -> compile error
let k2 = key.clone();    // Secret has no Clone  -> compile error
```

```go
s := string(key)         // compiles; copy lands on the GC heap
log.Printf("%v", key)    // compiles; copy lands wherever the logger puts it
cache[id] = key          // compiles; escapes to the heap inside an interface
```

`memguard` protects the copy you *remembered* to put in a `LockedBuffer`. The moment some
function does `string(key)`, that copy escapes the protection and **nothing tells you**.

**Summary:** capability is roughly equal; the difference is discipline versus compile-time
enforcement. On the specific residual risk in this threat model — hibernation writing RAM to
disk — neither language helps during the window when the key is legitimately alive, and
memguard's in-memory encryption actually shortens that window. Rust's edge is against *stray
copies that outlive their intended lifetime*, which is a real risk but a narrower one than
"Rust wins on memory hygiene."

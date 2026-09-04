# Enfold file formats v1

Byte-level specification. Read `DESIGN.md` first — this document assumes the key hierarchy and
the reasoning behind it, and specifies only serialisation.

Status: **draft, no implementation yet.** Nothing is frozen until v1 ships; after that, every
change needs a format version bump.

> **This document was rewritten on 2026-09-01.** An earlier draft described a single container
> holding slots, index and data together. That design was replaced: the keystore is now its own
> file and archives are separate, portable files. See `DECISIONS.md`.

---

## 1. Conventions

| | |
| --- | --- |
| Byte order | **Little-endian**, everywhere, no exceptions — including the STREAM chunk counter (§12), which deliberately diverges from age's big-endian choice. One rule with no carve-outs is worth more than interoperability we will never use |
| Integers | `u8`/`u16`/`u32`/`u64` unsigned; `i64` for timestamps |
| Timestamps | `i64`, Unix seconds UTC. Display and last-resort tie-breaking only — never a merge primitive |
| Strings | UTF-8, `u16` length-prefixed, no NUL terminator, no BOM |
| Public keys | Length-prefixed. P-256 uncompressed X9.62 (`0x04 ‖ X ‖ Y`, 65 bytes); X25519 raw (32 bytes) |
| Reserved fields | **MUST** be written as zero and **MUST** be ignored on read |
| Unknown records | A reader meeting an unknown record type **MUST** fail closed, never skip. Silently ignoring the unrecognised is how downgrade bugs happen |

## 2. Two file types

```
Keystore file  *.eks     one per device, default %LOCALAPPDATA%\Enfold\keystore.eks
  slots  +  encrypted registry of archives and their keys

Archive file   *.efd     many, anywhere — including untrusted places
  envelope  +  encrypted file index  +  data
```

`.eks` reads as **E**nfold **K**ey **S**tore; `.efd` as Enfold data. Neither extension is
load-bearing — both files begin with magic bytes (`ENFOLDK\x01`, `ENFOLDA\x01`) and identification
uses those, never the name. Extensions exist for the shell and the user, so a renamed file still
opens and a correctly-named impostor still fails.

Both are claimed elsewhere by obscure software — `.efd` by Parallels Desktop among others, `.eks`
by a niche BI product — which was accepted after checking: the alternatives were worse (`.enf` is
taken by Finale, EndNote and EnCase Forensic), and the magic bytes make a collision a shell
annoyance rather than a correctness problem.

**Archives are designed to live in relatively untrusted locations** — public cloud storage,
removable media, someone else's machine. Nothing that could unlock one is ever written into it.

An archive therefore contains:

| | Confidential? |
| --- | --- |
| `archive_id`, `KID` | **plaintext** — needed to find the right key without trial decryption |
| Per-file DEKs, wrapped under the archive key | wrapped; useless without the keystore |
| File index (names, sizes, hashes) | encrypted under the archive key |
| File data | encrypted under per-file DEKs |

The wrapped DEKs use a symmetric AEAD, so **an archive contains no asymmetric key material and
presents no target for Shor's algorithm**. Someone who steals only archives holds ciphertext and
identifiers; the only attack is brute force against a 256-bit key.

The cost of plaintext `archive_id` is **linkability**: an observer can tell that two archive files
are versions of the same archive. This is an accepted trade — the alternative is trial decryption
against every known key on every open.

## 3. Key hierarchy

```
VMK  (random 256-bit, memory only, never persisted)
 ├─ HKDF(info = "Enfold/v1/metadata" ‖ vault_id) → Metadata key    encrypts the registry
 ├─ HKDF(info = "Enfold/v1/db"       ‖ vault_id) → DB key          local caches
 ├─ HKDF(info = "Enfold/v1/wrap/archive"  ‖ vault_id) → KWK        wraps archive keys
 └─ HKDF(info = "Enfold/v1/wrap/identity" ‖ vault_id) → KWK_identity

archive key   (random 256-bit, one per archive VERSION, named by KID, stored wrapped in the keystore)
 ├─ HKDF(info = "Enfold/v1/archive/index" ‖ archive_id) → archive index key   encrypts the file list
 └─ HKDF(info = "Enfold/v1/archive/wrap"  ‖ archive_id) → archive wrap key    wraps per-file DEKs

per-file DEK  (random 256-bit, one per file, wrapped in the archive)
 └─ file content = STREAM(AES-256-GCM, DEK, [zstd(plaintext)])
```

Three levels rather than two. The middle one is what makes an archive portable but locked: it
carries everything except the single archive key, which lives only in the keystore.

### 3.1 Reaching the VMK — IK derivation, per slot type

All three end at `IK = HKDF-SHA256(pre, salt = ∅, info = "Enfold/v1/IK" ‖ vault_id ‖ recipient_id)`
and differ only in how `pre` is produced.

**Hardware slot** (`slot_type = 1`) — classical, see §16 for the accepted limitation.

```
H     = ECDH(SK_hw, epk)                          on the token, P-256, after PIN + touch
pwd'  = HMAC-SHA256(key = H, msg = user_password) entangled password, OPTIONAL
salt' = SHA-256(salt ‖ vault_id ‖ recipient_id)
pre   = Argon2id(pwd', salt', m, t, p) → 32 bytes
        …or simply pre = H when no entangled password is set (Argon2id skipped entirely)
```

**Recovery slot** (`slot_type = 3`) — **hybrid X25519 + ML-KEM-1024, post-quantum**

```
creation, from the 128-bit recovery key R:
  seed_x = HKDF(R, salt = slot_salt, info = "Enfold/v1/recovery/x25519" ‖ vault_id ‖ recipient_id) → 32 B
  seed_k = HKDF(R, salt = slot_salt, info = "Enfold/v1/recovery/mlkem"  ‖ vault_id ‖ recipient_id) → 64 B
  (sk_x, pk_x) = X25519 from seed_x
  (dk,   ek)   = ML-KEM-1024 from seed_k
  store pk_x and ek; destroy R, both seeds, sk_x and dk immediately

wrapping — offline, the recovery key need NOT be present:
  (e, E)    = fresh X25519 pair;  H_x = ECDH(e, pk_x);  destroy e
  (K_k, ct) = ek.Encapsulate()
  pre = HKDF(H_x ‖ K_k, salt = ∅, info = "Enfold/v1/recovery/combine" ‖ vault_id ‖ recipient_id)
  store E and ct

unlock — the user types the 48 digits:
  re-derive sk_x and dk from R
  H_x = ECDH(sk_x, E);  K_k = dk.Decapsulate(ct);  same pre
```

No Argon2id here: `R` is full entropy, so HKDF alone suffices.

**Standalone password slot** (`slot_type = 2`) — the same hybrid shape, with both seeds derived
from an Argon2id output over the password instead of from `R`. Argon2id is mandatory here, since
a user-chosen password is not full entropy.

### 3.2 Why the software slots are hybrid and the hardware slot is not

Both software slots are asymmetric so that VMK rotation can re-wrap them without the credential
present. That property has a cost that is easy to miss:

> A purely classical asymmetric slot hands a quantum attacker the private key **directly from the
> stored public key** — which means it bypasses the recovery key and the standalone password
> *entirely*. No grinding, no guessing.

For a user who has set an entangled password, that made the recovery slot a **free back door
around their own password**. Hybridising closes it: an attacker must break X25519 *and* ML-KEM to
recover `pre`.

The hardware slot cannot be given the same treatment, because the PQ half would have to run on the
token and no shipping YubiKey does ML-KEM (§16). Its `pre` still rests on P-256 alone — with the
entangled password, when set, as the remaining defence.

**Concrete parameters**, verified against Go 1.26's `crypto/mlkem` rather than taken from the spec
document: seed 64 bytes, encapsulation key 1568 bytes, ciphertext 1568 bytes, shared key 32 bytes,
and key generation from a seed is deterministic — which is what makes derive-from-`R` possible at
all. ML-KEM-1024 rather than the library's recommended 768 because 768 is roughly AES-192 while
everything else here is 256-bit; the size difference is irrelevant inside a 128 KiB slot region.

`KWK` and `KWK_identity` are **separate wrapping domains on purpose**. The low-trust-PC channel
exists to release individual archive keys; if the sync identity private key were wrapped in the
same domain, an over-broad request or an indexing bug could dispense it. Separate domains make
that structurally impossible rather than something the code must remember not to do.

### 3.3 Byte-exact rules

Everything above reads unambiguously to a person and ambiguously to two implementers. Each rule
below is a place where two reasonable implementations would otherwise diverge — and every wrong
choice still yields 32 plausible bytes, so nothing but a test vector would ever notice.
`testdata/kdf-vectors.json` pins each of these.

**R1 — HKDF.** Always HKDF-SHA256 as Extract-then-Expand (RFC 5869). `salt = ∅` means a
zero-length salt, which Extract treats as 32 zero bytes. Output length is 32 bytes unless a rule
says otherwise. Go: `hkdf.Key(sha256.New, ikm, salt, info, n)`.

**R2 — Concatenation.** `‖` is raw byte concatenation. No separators, no length prefixes, no
padding. `info = "Enfold/v1/IK" ‖ vault_id ‖ recipient_id` is exactly 12 + 16 + 16 = 44 bytes.

**R3 — Info strings.** ASCII, no terminator. The complete registry:

| Derives | `info` prefix | IKM | salt | out |
| --- | --- | --- | --- | --- |
| `IK` from `pre` | `Enfold/v1/IK` ‖ vault_id ‖ recipient_id | `pre` | ∅ | 32 |
| `seed_x`, recovery | `Enfold/v1/recovery/x25519` ‖ vault_id ‖ recipient_id | `R` | `slot_salt` | 32 |
| `seed_k`, recovery | `Enfold/v1/recovery/mlkem` ‖ vault_id ‖ recipient_id | `R` | `slot_salt` | 64 |
| `pre`, recovery | `Enfold/v1/recovery/combine` ‖ vault_id ‖ recipient_id | `H_x ‖ K_k` | ∅ | 32 |
| `seed_x`, password | `Enfold/v1/password/x25519` ‖ vault_id ‖ recipient_id | `A` | `slot_salt` | 32 |
| `seed_k`, password | `Enfold/v1/password/mlkem` ‖ vault_id ‖ recipient_id | `A` | `slot_salt` | 64 |
| `pre`, password | `Enfold/v1/password/combine` ‖ vault_id ‖ recipient_id | `H_x ‖ K_k` | ∅ | 32 |
| Metadata key | `Enfold/v1/metadata` ‖ vault_id | VMK | ∅ | 32 |
| DB key | `Enfold/v1/db` ‖ vault_id | VMK | ∅ | 32 |
| `KWK` | `Enfold/v1/wrap/archive` ‖ vault_id | VMK | ∅ | 32 |
| `KWK_identity` | `Enfold/v1/wrap/identity` ‖ vault_id | VMK | ∅ | 32 |
| archive index key | `Enfold/v1/archive/index` ‖ archive_id | archive key | ∅ | 32 |
| archive wrap key | `Enfold/v1/archive/wrap` ‖ archive_id | archive key | ∅ | 32 |

The recovery and password slots use **distinct** strings even though their shapes are identical.
Domain separation is free, and it forecloses any construction in which one slot type's output
could be mistaken for another's.

**R4 — Passwords.** A password is the **UTF-8 encoding of the NFC-normalised string**. Not NFKC,
not the raw code points the platform happened to produce. The same passphrase typed on two
machines with different input methods must derive the same key, and combining sequences are the
usual way that fails. **An empty password is rejected at input**; "no entangled password" is a
state recorded by `flags` bit0, never inferred from length.

**R5 — Raw ECDH outputs.** P-256 ECDH output is the **32-byte big-endian X coordinate** of the
shared point, exactly as `crypto/ecdh` returns it. X25519 output is likewise the **raw 32-byte
u-coordinate**. Nothing is hashed at this stage on either curve; `H` feeds the HMAC fold (or, with
no password, HKDF) directly, and `H_x` feeds the combine step directly.

**R6 — Argon2id.** Version `0x13`. `m` is in **KiB**. The number of threads used equals `p`
exactly — an implementation may not "helpfully" use more cores, because `p` is part of the
function. Output 32 bytes. Go: `argon2.IDKey(pwd, salt, t, m, p, 32)`.

**R7 — The HMAC fold.** `pwd' = HMAC-SHA256(key = H, msg = P)` with `H` (32 bytes) as the key and
the R4-encoded password as the message. Not the other way round.

**R8 — The standalone password slot in full.** This was under-specified above; it is:

```
P      = UTF-8(NFC(password))                       R4
salt'  = SHA-256(salt ‖ vault_id ‖ recipient_id)
A      = Argon2id(P, salt', m, t, p) → 32           no HMAC fold — there is no H to fold with
seed_x = HKDF(A, slot_salt, "Enfold/v1/password/x25519" ‖ …) → 32
seed_k = HKDF(A, slot_salt, "Enfold/v1/password/mlkem"  ‖ …) → 64
```

and from there identical to the recovery slot, using the `password/combine` info.

**R9 — Seeds to keypairs.** `seed_x` (32 bytes) is the X25519 private scalar as-is; clamping is
the function's job, not the caller's. Go: `ecdh.X25519().NewPrivateKey(seed_x)`. `seed_k` (64
bytes) is the FIPS 203 `d ‖ z` seed. Go: `mlkem.NewDecapsulationKey1024(seed_k)`. Both are
deterministic — the same seed must always give the same public key, and a vector checks it.

**R10 — Hybrid combine.** The IKM is `H_x ‖ K_k` in that order: the 32-byte X25519 shared secret,
then the 32-byte ML-KEM shared key. 64 bytes.

**R11 — Recovery key bytes and digits.** `R` is 16 bytes, read as **8 little-endian `u16`
values** `v₀ … v₇`. Digit group `i` is `v_i × 11`, zero-padded to 6 digits; groups are printed in
order, separated by `-`. Decoding checks each group is < 720 896 and divisible by 11 before
dividing. This fixes the byte order the BitLocker description leaves open; no claim of
compatibility with BitLocker's own key material is made or wanted.

**R12 — `wrapped_vmk` plaintext.** `VMK ‖ u64 vmk_generation`, the integer little-endian per
§1. 40 bytes in, 56 out with the tag. AES-256-GCM **keyed by that slot's `IK`**, 12-byte nonce,
16-byte tag appended.

**R13 — Two salts, and which is which.** A slot record carries two 32-byte salts with different
jobs, and the prose above uses the bare word "salt" for one of them:

| Field | Used as |
| --- | --- |
| `salt` | Input to `salt' = SHA-256(salt ‖ vault_id ‖ recipient_id)`, the Argon2id salt for hardware and password slots |
| `slot_salt` | The HKDF salt when deriving `seed_x` and `seed_k` in software slots (R3, R8) |

Wherever §3.1 or R8 writes `salt` unqualified, it means the `salt` field. The standalone password
slot's Argon2id parameters are the slot record's own `argon2_m`, `argon2_t`, `argon2_p` — the same
fields a hardware slot with an entangled password uses.

**A note on what the vectors can and cannot catch.** These rules were checked by having an
independent implementation written from this section and `testdata/kdf-inputs.json` alone, with
no access to the reference generator; it matched the reference on every one of 63 values. R13 and
the X25519 half of R5 exist because that implementer reported having to guess them — correctly,
as it turned out, but a guess is a guess.

---

# Part I — Keystore file

## 4. Keystore layout

```
0x00000  Superblock A          4 KiB
0x01000  Superblock B          4 KiB
0x02000  Slot region A       128 KiB
0x22000  Slot region B       128 KiB
0x42000  Registry              encrypted; offset and length in the superblock
```

The keystore is small — a few MB even with thousands of archives — so the registry is **rewritten
wholesale** on every change and lands in one superblock flip. **No allocator is needed here.**

Superblocks alternate: write the one that is not live, fsync, and it becomes live by carrying the
higher `seq`. A reader takes the valid superblock with the higher `seq`; "valid" means the
checksum verifies.

### Why the slot region is doubled too

Slot mutations must be crash-atomic, and a VMK rotation rewrites **every** slot record with a
fresh `epk` and `wrapped_vmk`. Overwriting in place would let a crash leave a corrupt slot region
and an unopenable keystore.

Allocating a second landing site from a free map was rejected: the registry is encrypted and needs
the Metadata key, while slot writes happen around unlock and rotation. Doubling the region instead
reuses the superblock argument one level down and needs no new correctness reasoning:

```
1. write the new slot region into the inactive copy, fsync
2. write the inactive superblock with seq+1 pointing at it, fsync
```

Record sizes differ by an order of magnitude between slot types, so size the region for the worst
case:

| Slot type | Record size | Why |
| --- | --- | --- |
| Hardware (PIV) | ~250 bytes | one P-256 `epk`, one `slot_pubkey`, no credential ID |
| Recovery / standalone password | **~3.3 KB** | hybrid: `ek` 1568 + `ct` 1568, plus the X25519 halves (§3.1) |
| `prf-derived` *(reserved)* | up to ~1.3 KB | a FIDO credential ID may reach 1023 bytes |

**`slot_count` is capped at 32**, and 32 hybrid slots is ~106 KB, so **128 KiB per copy** covers
even a pathological configuration with room left. A realistic vault — two hardware keys, one
password, one recovery — is about 7 KB.

The cap is a judgement rather than a technical limit: a vault with 32 unlock methods is a
misconfiguration, and each extra slot adds to the cost of reasoning about the invariant.

## 5. Keystore superblock

Fixed 4096 bytes. Everything before `checksum` is covered by it.

| Field | Type | Notes |
| --- | --- | --- |
| `magic` | `u8[8]` | ASCII `ENFOLDK\x01` |
| `format_version` | `u16` | `1` |
| `reserved0` | `u16` | |
| `seq` | `u64` | Monotonic; higher valid superblock wins |
| `vault_id` | `u8[16]` | Random at creation, **immutable**. In HKDF info strings and AADs |
| `slot_region_off` | `u64` | Which slot region copy is live |
| `slot_region_len` | `u64` | |
| `registry_off` | `u64` | |
| `registry_len` | `u64` | Ciphertext length, excluding tag |
| `registry_nonce` | `u8[12]` | |
| `registry_tag` | `u8[16]` | |
| `vmk_generation` | `u64` | Incremented on each VMK rotation |
| `rotation_pending` | `u8` | Non-zero while a rotation has been deferred or is incomplete (§8) |
| `reserved1` | `u8[…]` | Zero-filled to 4064 |
| `checksum` | `u8[32]` | SHA-256 over bytes `[0, 4064)` |

**Checksummed, not authenticated.** It must be readable before unlocking, so no key exists to MAC
it. Integrity of what matters comes from the registry AEAD instead: `registry_off`,
`registry_len`, `registry_nonce` and `vault_id` are all in the registry's AAD, so editing them
causes an authentication failure rather than a silent misread.

## 6. Slot region

```
u32   slot_count
u32   reserved
then slot_count records, each prefixed with u32 record_len
```

| Field | Type | Notes |
| --- | --- | --- |
| `record_len` | `u32` | |
| `slot_state` | `u8` | `0` empty · `1` active · `2` retired |
| `slot_type` | `u8` | `1` external-ECDH · `2` standalone-password · `3` recovery |
| `key_source` | `u8` | For type 1: `1` yubikey-piv · `2` prf-derived *(reserved)* · `3` phone-native |
| `curve_id` | `u8` | `1` P-256 · `2` X25519 |
| `recipient_id` | `u8[16]` | Random, stable for the slot's life; used in HKDF info and AAD |
| `flags` | `u32` | bit0 `has_entangled_password` · bit1 `prf_raw_salt_mode` · bit2 `uv_required` · bit3 `rewrap_stale` (§8) |
| `label` | `string` | Display only — never matched on |
| `created_at` | `i64` | |
| `epk` | `pubkey` | Per-slot ephemeral public key; regenerated on every re-wrap |
| `slot_pubkey` | `pubkey` | `P`, present for all asymmetric slots; **also the verifier** |
| `salt` | `u8[32]` | Argon2 salt |
| `argon2_m` | `u32` | KiB |
| `argon2_t` | `u32` | |
| `argon2_p` | `u8` | Part of the algorithm — changing it changes the output |
| `slot_salt` | `u8[32]` | Seed derivation and domain separation for derived-keypair slots (§3.1) |
| `mlkem_ek` | `bytes` (`u16` len) | ML-KEM-1024 encapsulation key, 1568 B. Software slots only; empty for hardware slots |
| `mlkem_ct` | `bytes` (`u16` len) | ML-KEM ciphertext from the last wrap, 1568 B. Regenerated on every re-wrap alongside `epk` |
| `credential_id` | `bytes` (`u16` len) | FIDO credential ID; empty for PIV slots |
| `wrap_nonce` | `u8[12]` | |
| `wrapped_vmk` | `u8[56]` | AES-256-GCM over `VMK ‖ u64 vmk_generation`: 40 bytes ciphertext ‖ 16 bytes tag |

### 6.1 AAD for `wrapped_vmk`

**Every byte of the record from `slot_state` through `wrap_nonce`, with `wrapped_vmk` zeroed**,
plus `vault_id`.

The Argon2 parameters cannot be encrypted — they must be read before any key exists — but they
must be authenticated, or an attacker rewrites `argon2_m` from 1 GiB to 8 KiB and brute-forces
cheaply. Same for `epk`, `salt`, `flags`, `mlkem_ek` and `mlkem_ct`.

### 6.2 `vmk_generation` travels *inside* the wrapped blob

`wrapped_vmk` encrypts `VMK ‖ u64 vmk_generation`, not the VMK alone. Inside the plaintext rather
than in the AAD, deliberately: AEAD authenticates its plaintext just as firmly, and this way a
successful unwrap **tells you** the generation instead of requiring you to know it in advance.

It answers two things at once.

**Splicing.** An attacker with write access to the keystore — plausible, since backups exist — can
otherwise paste an old slot region back, point the superblock at it, and keep the current registry:
a deleted slot springs back to life. Binding each `wrapped_vmk` to a generation makes such a record
detectably stale the moment the VMK has been rotated. It does **not** help while the VMK is
unrotated, since the generation is then unchanged; the real defence there is to rotate, which is
why rotation now defaults to on (§8).

**Diagnosis.** Unlocking with a slot left stale by a deferred rotation would otherwise look like
corruption: the verifier passes, the unwrap succeeds, and the registry then fails to decrypt
because the recovered VMK is the previous one. Comparing the recovered generation against the
superblock's turns that into an accurate message — *this credential is behind; unlock another way
first to bring it up to date* — instead of a false corruption report.

### 6.3 `slot_pubkey` doubles as a verifier

After deriving a slot's private scalar, compute `d·G` and compare with `slot_pubkey` **before**
attempting to unwrap. This separates two failures that would otherwise both read as "decryption
failed": a mismatch means *this authenticator no longer produces this slot's key*, which is
actionable while other slots still work; a match with a failed unwrap means corruption.

For hybrid software slots, verify the X25519 half this way and let the ML-KEM half be checked by
the AEAD — a wrong `dk` yields a wrong shared key, a wrong `pre`, a wrong `IK` and an
authentication failure, with no separate verifier needed.

Be aware *why* the AEAD is the only place that failure can surface: **ML-KEM uses implicit
rejection.** Decapsulating with the wrong `dk` does not return an error; it returns a different,
perfectly valid-looking 32-byte key. Nothing in the ML-KEM step itself will ever signal a problem.
Code that expects `Decapsulate` to fail on a bad key is waiting for something that cannot happen.

### 6.4 Slot invariant

Before committing any slot mutation, evaluate: **there exist two active slots whose
required-secret sets are disjoint** (`DESIGN.md` §5). A mutation that would break it is refused.
A predicate over the whole set, not a per-record check.

## 7. Registry

One AES-256-GCM ciphertext under the Metadata key.

```
AAD = vault_id ‖ registry_off ‖ registry_len ‖ registry_nonce ‖ format_version
```

Plaintext:

```
u32    registry_version        1
u8[16] device_id               this replica's stable identity (SYNC.md)
i64    modified_at
u8[48] wrapped_identity_key    device identity X25519 private key, under KWK_identity
u8[12] identity_nonce
u32    archive_count
       … archive records
u32    peer_count
       … peer pin records
```

### 7.1 Archive record

One per archive, carrying **all of its versions**.

| Field | Type | Notes |
| --- | --- | --- |
| `archive_id` | `u8[16]` | Stable identity. **Never match on filename** |
| `name` | `string` | The trusted name — see §7.4 |
| `last_path` | `string` | Where it was last seen. A hint, never an identity |
| `policy` | `u32` | bit0 `always_require_full_auth` (ignores the session cache) |
| `created_at` | `i64` | |
| `current_kid` | `u8[16]` | Which version record is in use now |
| `last_ciphertext_hash` | `u8[32]` | SHA-256 of the whole archive file as last written by the manager |
| `last_stored_size` | `u64` | |
| `last_written_at` | `i64` | |
| `revision` | `u64` | Merge (SYNC.md §5) |
| `last_writer` | `u8[16]` | Merge |
| `version_count` | `u32` | |
| … | | version records |

**`last_ciphertext_hash` is over the ciphertext on purpose**, and it is computable with **no keys
at all**. Plugging in a USB stick is enough to answer "is this copy the current one, and is it
intact?" without unlocking anything. That is a different question from the per-file **plaintext**
hashes inside the archive (§11), which verify that a decryption produced the right bytes. Both
exist; do not conflate them.

It lives on the archive record rather than on a version record because the ciphertext changes on
every edit while the archive key does not — see §7.2.

### 7.2 Version record

A version is **a key, not a snapshot**.

| Field | Type | Notes |
| --- | --- | --- |
| `kid` | `u8[16]` | **Random**, never sequential — see below |
| `wrapped_archive_key` | `u8[48]` | Under `KWK` |
| `wrap_nonce` | `u8[12]` | |
| `created_at` | `i64` | |
| `retired_at` | `i64` | Zero while current |
| `state` | `u8` | `1` current · `2` retired |

**KIDs must be random, not sequential.** The reason is coordination, not privacy: archives get
created independently on several devices, and a counter would collide without a shared allocator.
128 random bits are unique without anyone having to agree on anything. A secondary benefit is that
a counter appearing in plaintext in every envelope would leak how many keys exist — but do not
justify it by "hiding creation order", which it does not achieve: the filesystem's mtime already
exposes that.

**Editing a file does not create a version.** Editing mints a fresh **per-file DEK** (§12) and
leaves the archive key and KID untouched. A new version appears only when the **archive key is
deliberately rotated**, which is rare. Version count therefore tracks rotations, not edits.

**Retired keys are retained deliberately.** After a rotation, any copy of the archive made earlier
— on a USB stick, in cloud storage — is still encrypted under the previous archive key. Drop that
key and the backup becomes permanently unopenable while the file sits there intact.

Retention still widens the compromise surface over time, so the UI should show the count and offer
pruning — "3 retired keys, needed to open older copies of this archive" — and let the user make
that trade knowingly. Because rotations are rare, the list stays short in normal use.

### 7.3 Rotating an archive key

Rotation mints a new archive key and a new KID, re-derives the archive index key and archive wrap
key, **re-wraps every per-file DEK under the new archive wrap key, and rewrites the archive's
index. No file data is re-encrypted.** The cost is proportional to the file count, not the archive
size — the same trick as VMK rotation, one level down.

The motivating case is the low-trust-PC mode: **that PC received the archive key.** Rotating it
afterwards is how access is withdrawn. As everywhere else in this design, it withdraws future
access — the copy that machine may have kept remains readable under the old key, and no
construction can change that.

### 7.4 The name in the registry is the trusted one

Two names exist for every archive: the one in this record, and the filename on disk.

```
filename on disk       anyone can change it, no key required
name in the registry   encrypted; unchangeable without unlocking the keystore
```

**Every authorisation prompt shows the registry name.** Otherwise malware renames
`tax-records.efd` to `photos.efd` and the user approves "open photos" without a second
thought. Using the registry name closes that path, at the cost that a prompt can show a stale name
after an out-of-band rename. That cost is worth paying: a trusted source overriding an untrusted
one is exactly what is wanted.

Update policy:

- **Renamed through the manager**, keystore local and metadata unlocked → update the registry
  directly, no ceremony. The change came through the trusted path.
- **Mismatch detected on disk** → surface it ("this archive was renamed to *photos*") rather than
  silently absorbing it, and only after a successful authorised open. A rename is either the
  user's own doing, in which case they glance and move on, or it is not, in which case they should
  see it.

### 7.5 Peer pin record

```
pubkey  peer_identity_pubkey    X25519; the ONLY thing matched on
string  device_name             display only, attacker-chosen, never matched
i64     paired_at
i64     last_seen_at
u32     capabilities            bit0 open_archive · bit1 append_dek · bit2 full_sync
u8      device_class            1 ephemeral-session · 2 enrolled-personal
```

## 8. VMK rotation

**Offered on every slot removal and credential change, with rotation pre-selected.** `DESIGN.md`
§5 covers why the default sits there rather than on "skip". The mechanics:

1. Generate `VMK'`, increment `vmk_generation`, derive the four subordinate keys.
2. Unwrap every archive key with `KWK`, re-wrap with `KWK'`. **No archive file is touched** — the
   archive keys themselves are unchanged, only their wrappers.
3. Re-wrap the identity private key under `KWK_identity'`.
4. Re-wrap `VMK' ‖ vmk_generation` into every surviving slot with a fresh `epk` — and, for hybrid
   software slots, a fresh ML-KEM encapsulation, so `mlkem_ct` is replaced too.
5. Write the slot region, then the registry, then flip the superblock.

The whole rotation lands in **one superblock flip**, so it is atomic: a crash leaves the old VMK
and old registry fully intact. There is never a moment when some archive keys are under the new
`KWK` and others under the old, which is why no per-record generation tag is needed on archive
records.

Cost is roughly 60 bytes per **archive**, not per file, because only archive keys are re-wrapped.
A vault with a thousand archives rotates in well under a second.

### Deferred and partial rotation

Step 4 needs each slot's key material. Every v1 slot type is asymmetric — hardware slots use the
stored `slot_pubkey`, software slots the stored `slot_pubkey` plus `mlkem_ek` — so **no credential
need be physically present** and rotation normally completes in full. A future symmetric hardware
slot (§16) would be the exception.

When a slot genuinely cannot be re-wrapped, it is marked `rewrap_stale` and its `wrapped_vmk` is
**left exactly as it is**, which means that record alone still carries the *previous* VMK and its
generation number.

> This is the one place a VMK survives a rotation, and it is worth stating plainly because §8
> otherwise says no VMK history is retained. The history is not a separate structure: it is the
> untouched `wrapped_vmk` of a stale slot, reachable only by that slot's own credential.

`rotation_pending` in the superblock stays non-zero while any slot is stale, and **the UI must
keep surfacing it** — a deferred rotation that is never finished is the same as no rotation.
Unlocking with a stale slot is detected by the generation comparison in §6.2 and reported as
*this credential is behind*, never as corruption.

Revocation is not weakened: the removed slot's record is deleted, so a removed credential cannot
reach the retained old VMK through the current keystore.

---

# Part II — Archive file

## 9. Archive layout

```
0x0000  Envelope              4 KiB, plaintext
0x1000  Superblock A          4 KiB
0x2000  Superblock B          4 KiB
0x3000  File index            encrypted under the archive index key
   …    Free-space map
   …    Data region           per-file chunk streams
```

Unlike the keystore, an archive **does** need a free-space map. Editing one file inside a 50 GB
archive must rewrite that file's extent, not the archive — that is the whole reason for per-file
DEKs (`DESIGN.md` §8).

## 10. Envelope

Plaintext, 4096 bytes, readable by anyone holding the file. It says only what is needed to find
the right key.

| Field | Type | Notes |
| --- | --- | --- |
| `magic` | `u8[8]` | ASCII `ENFOLDA\x01` |
| `format_version` | `u16` | `1` |
| `reserved0` | `u16` | |
| `archive_id` | `u8[16]` | Which archive this is |
| `kid` | `u8[16]` | Which archive key opens it |
| `reserved1` | `u8[…]` | Zero-filled to 4064 |
| `checksum` | `u8[32]` | SHA-256 over `[0, 4064)` |

**Nothing here is confidential and nothing here is trusted.** The manager uses `kid` to look up a
key and everything after that is authenticated by AEAD. An attacker editing the envelope achieves
a failed lookup, not a misdirected decryption.

**No `vault_id`, deliberately.** An earlier draft carried one so the manager could "fail fast and
clearly", and it was wrong twice over. It **breaks sync**: every device has its own keystore with
its own `vault_id` (`SYNC.md` §1), so a phone that receives an archive key legitimately would find
a PC's `vault_id` in the envelope and reject a file it can perfectly well open. And if the field
were therefore not checked, all it would do is **link every archive a person owns, wherever it is
stored, to one identity, and each of them to a specific keystore file** — a far wider leak than the
per-archive linkability accepted in §2. A failed `kid` lookup is already a fast, clear failure;
looking up `archive_id` in the registry distinguishes "not from this keystore" from "corrupt".

**No `created_at` either.** It contributes nothing to finding the key, sits in plaintext where it
is not authenticated, and duplicates what the filesystem's mtime already exposes. The authenticated
copy lives in the encrypted index.

## 11. Archive superblock and file index

The superblock mirrors §5 in structure — `seq`, offsets and lengths for the index and free map,
a checksum — and alternates the same way, so an edit is atomic.

The file index is one AES-256-GCM ciphertext under the **archive index key**.

```
AAD = archive_id ‖ kid ‖ index_off ‖ index_len ‖ index_nonce ‖ format_version
```

Plaintext: a `zstd` trained dictionary (optional, for many small files) followed by file records.

| Field | Type | Notes |
| --- | --- | --- |
| `file_id` | `u8[16]` | Stable across edits; the identity used in merge |
| `state` | `u8` | `1` live · `2` tombstone — the record survives deletion so a sync cannot resurrect it |
| `name` | `string` | |
| `orig_size` | `u64` | Plaintext length; also tells the reader which chunk is final |
| `stored_size` | `u64` | |
| `storage` | `u8` | `1` raw · `2` zstd · `3` zstd + dictionary |
| `content_hash` | `u8[32]` | SHA-256 of the **plaintext** |
| `data_off` | `u64` | |
| `chunk_size` | `u32` | 65536 for v1 |
| `alg_id` | `u16` | `1` AES-256-GCM · `2` ChaCha20-Poly1305 *(reserved)* |
| `dek_nonce` | `u8[12]` | |
| `wrapped_dek` | `u8[48]` | Under the **archive wrap key** |
| `dek_epoch` | `u32` | Incremented each time this file's DEK is replaced |
| `dek_created_at` | `i64` | |
| `pack_id` | `u8[16]` | Reserved for small-file packing; all-zero when unused |
| `revision` | `u64` | Merge |
| `last_writer` | `u8[16]` | Merge |
| `modified_at` | `i64` | Display and last-resort tie-break |

`content_hash` is over the **plaintext** because that is the question the AEAD does not already
answer: whether the content changed as distinct from whether the key changed (`dek_epoch`), and
whether what came out equals what went in — which catches bugs in this project's own compression
and chunking rather than attacks. Corruption of the ciphertext is already caught by the GCM tags.

## 12. Data region

Each file's content is an independent STREAM-chunked AEAD blob:

- Nonce = 11-byte **little-endian** counter ‖ 1-byte final flag (`0x00` / `0x01`), following the
  STREAM construction of age and Tink but with this project's byte order (§1). The counter
  prevents reordering; the final flag prevents truncation. **Reaching the end of an extent
  without decrypting a chunk marked final is an error.**
- 65536 bytes of plaintext per chunk plus a 16-byte tag; the last chunk is short.
- AAD per chunk: `archive_id ‖ file_id ‖ alg_id ‖ chunk_size`.

**Any modification to an existing file mints a fresh DEK** and rewrites its extent. Never reuse a
`(DEK, counter)` pair — reuse is catastrophic for GCM. Always minting a new key makes the mistake
structurally impossible rather than merely avoided by care.

**This is the per-file DEK only.** The archive key and its KID are untouched by an edit; they
change only on a deliberate archive-key rotation (§7.3). The two levels rotate on completely
different schedules and for completely different reasons, and conflating them is the easiest
mistake to make in this design:

| | Changes when | Why |
| --- | --- | --- |
| **per-file DEK** | every edit of that file | nonce reuse would be catastrophic |
| **archive key / KID** | explicit rotation only | withdrawing access someone was granted |

`dek_epoch` on the file record counts the former. `version_count` on the archive record counts the
latter.

## 13. Free-space map

```
u32   extent_count
then per extent:  u64 offset, u64 length
```

First-fit with coalescing on free. Rewritten with the index and covered by the same superblock
flip, so it is always consistent with the extents the index references.

Compaction is an explicit offline operation. It is the only operation that moves file data without
changing a DEK, which is permissible because ciphertext bytes are copied verbatim rather than
re-encrypted.

---

## 14. Algorithm identifiers

Assign once; never reuse a number, never renumber.

| Registry | ID | Meaning |
| --- | --- | --- |
| `alg_id` | 1 | AES-256-GCM |
| `alg_id` | 2 | ChaCha20-Poly1305 *(reserved — better on ARM)* |
| `curve_id` | 1 | NIST P-256 |
| `curve_id` | 2 | X25519 |
| `storage` | 1 | raw · 2 zstd · 3 zstd + trained dictionary |
| `slot_type` | 1 | external-ECDH · 2 standalone-password · 3 recovery |
| `key_source` | 1 | yubikey-piv · 2 prf-derived *(reserved)* · 3 phone-native |

`slot_type = 1` covers all three key sources deliberately: they produce **identical slot records**
— `{epk, slot_pubkey, wrapped_vmk}` — and share the offline re-provisioning path verbatim. That is
what makes the backend choice reversible rather than architectural.

## 15. Losing the keystore

**Without the keystore, every archive is permanently unopenable**, however intact the files are.

The recovery key does not help: it unlocks *a* keystore, it does not reconstruct one. These two
are extremely easy to confuse and the confusion only surfaces at the worst possible moment, so the
UI must state it plainly.

**Keystore backup is a separate concern from the recovery key**, and its design is **deferred**.
An earlier draft called automatic backup a mandatory feature; that was premature. Automatic backup
raises questions this project cannot answer offline — when to write a copy, where to write it, and
how stale copies are reconciled — and it only really coheres once there is an online service to
own those answers. For v1 the practical guarantee is the slot invariant: **at least two
independent ways in** (§6.4).

What v1 *should* ship is a **manual export**, with overwrite or incremental update of a chosen
target. That is achievable offline and puts the user in control of the three hard questions.

### What goes in an export — the question with a real answer

The obvious options split badly:

| Contents | Restorable? | Exposure |
| --- | --- | --- |
| Registry only | **No.** Archive keys are wrapped under `KWK`, which needs the VMK, which needs a slot | low |
| Registry + all slots | Yes | **The entire keystore** — precisely the artefact §16 says not to let out, and the input to the splice attack in §6.2 |

**A useful backup must carry a slot, and carrying the hardware slots is what makes it dangerous.**
The way out is to carry exactly one:

> **Export = registry + the recovery slot only.** No hardware slots, no standalone password slot.

This is restorable — the recovery key opens it, yielding the VMK, from which a fresh keystore is
rebuilt and the YubiKeys re-enrolled. It is **post-quantum safe**, because the recovery slot is the
hybrid one (§3.1) and no classical-only slot material travels with it. And it is not useful to
splice, since the slot it carries cannot be opened without the recovery key, and anyone holding
that has no need to splice anything.

**Where the export goes is still a security decision**, because it remains a copy of the registry:

| Target | Verdict |
| --- | --- |
| Second local drive, or removable media | **Recommended** — with BitLocker To Go on the removable one |
| A machine the user controls, over the LAN | Fine |
| Public cloud storage | **Discouraged**, though far less catastrophic than exporting the full keystore would be |

An export must also be **verifiable**: the user needs a way to confirm the file will actually open
*before* they need it to. An untested backup is a belief, not a backup.

## 16. Accepted limitation: the hardware slot is not post-quantum

The software slots are hybrid X25519 + ML-KEM-1024 (§3.1) and need no further protection. **The
hardware slot is not, and cannot be in v1**: the PQ operation would have to run on the token, and
no shipping YubiKey performs ML-KEM. Yubico has shown a prototype, but it does post-quantum
*signatures* rather than a KEM, and it is not commercial.

That the constraint is temporary matters, because it argues against contorting the design around
it. **ML-KEM is asymmetric**, so encapsulation needs only the stored public key — a PQ hardware
slot would be post-quantum *and* keep the offline re-provisioning this design depends on. The
symmetric alternative (FIDO2 `hmac-secret`) would buy post-quantum security only by destroying
that property permanently, to work around a hardware gap that is expected to close. When a PIV
applet ships ML-KEM, adopting it costs one new `slot_type` and one new `alg_id`; the slot region
is already sized for it.

Until then, the exposure is bounded and specific:

- **Archives are unaffected.** They contain no asymmetric material (§2), so a stolen archive is
  brute-force-only, now and later.
- **The keystore is the whole of the exposure.** Someone who copies it today and later gains a
  cryptographically-relevant quantum computer recovers `H` from the stored `epk` or `slot_pubkey`.

Hence the operational guidance, which belongs in the product and not only in this file:

1. **Do not put the keystore in public cloud storage.** This is the single highest-value piece of
   advice in the document, and §15 aligns the backup defaults with it.
2. **Encrypt the storage layer with BitLocker** — the system volume for the keystore and local
   archives, BitLocker To Go for USB and removable backups. Note precisely what this does and does
   not do: **BitLocker is volume-level**, so a file copied to a cloud folder leaves the protected
   volume in the clear. It defends against a stolen laptop or a lost USB stick, not against upload.
3. Set an **entangled password** on high-value containers. It is the only thing standing between a
   harvested keystore and a future quantum adversary, which is a far more concrete reason to
   recommend it than "defence in depth".

BitLocker also quietly repays the metadata leakage accepted in §2: to anyone holding the disk, the
number of archives, their sizes and their filenames disappear along with everything else.

## 17. Open

- Retention policy for retired archive versions: a default count, a size budget, or manual only.
- Whether to reserve space for a post-quantum KEM in the slot record. Current answer: no — slot
  records are variable-length and carry an algorithm ID, so a new `slot_type` and `curve_id` value
  is all a future KEM would need.
- Whether the archive superblock needs the full 4 KiB or can be smaller for archives holding one
  small file.

# Enfold device pairing and sync

How two Enfold keystores talk to each other, and how a machine that holds no keys is granted access to a
single archive without being handed the whole keystore.

Read `DESIGN.md` for the key hierarchy and `FORMAT.md` for the on-disk records this refers to.

Status: **draft, no implementation yet.**

---

## 1. Scope — what syncs, and what deliberately does not

| | Syncs |
| --- | --- |
| File and archive metadata | **yes** |
| Archive keys | **yes** |
| Slots (YubiKey, password, recovery key) | **no** |
| VMK | **no** |

**Each *enrolled* device has its own keystore, its own VMK, and its own unlock methods.** A machine
must have a keystore of its own before it can take part in sync — though it needs no YubiKey to
get one, since standalone password plus recovery key is an existing tier.

A machine with no keystore is not excluded; it takes part as a **temporary device session** (§2),
which can be handed one archive's key but never syncs.

This scoping is not a limitation, it is the thing that makes sync tractable. Three hard problems
disappear:

**No slot merge, therefore no invariant violation.** Otherwise this is reachable, and both halves
are individually legal:

```
Device A: {YK#1, YK#2, recovery} → removes recovery   ✅ two keys remain
Device B: {YK#1, YK#2, recovery} → removes YK#2       ✅ key + recovery remain
merged:   {YK#1}                                      ❌ invariant broken
```

**No cross-device VMK coordination.** A device rotates its VMK whenever it removes a slot; no
peer needs to know or care. Revocation becomes a purely local operation.

**No shared-secret lifetime problem.** Compromise of one device's slots says nothing about
another's.

### Consequence for the wire format

Because the two sides have different KWKs, sync **unwraps each archive key on the sender and
re-wraps it on the receiver**. The key value itself is unchanged; only the wrapper differs.

**Per-file DEKs are not synced at all** — they live inside the archive file, wrapped under that
archive's own wrap key, and travel with the file. One archive key is all a peer needs.

**So the channel carries bare archive keys.** That single fact sets the requirements for
everything in §4: end-to-end encryption is mandatory, and so is forward secrecy — a recorded
session that becomes decryptable later would yield the keys to everything ever synced.

## 2. Device classes

Capabilities live in the pin record (`FORMAT.md` §7.5), not in per-request logic.

The distinction is **whether the machine has a keystore of its own**, not whether it is "public".
Someone may simply prefer not to keep keys on a particular computer, which is a legitimate choice
and has nothing to do with who else uses it. The UI should say **"temporary device session"**, never
"public computer".

| | Temporary device session | Enrolled device |
| --- | --- | --- |
| Has its own keystore | no | yes |
| Open a named archive (receive its archive key) | yes | yes |
| Append a new archive + its key | yes | yes |
| Receive the full keystore | **structurally impossible** | yes |
| Modify slots | no | no — slots never cross the wire |
| Remembered across sessions (persistent pin) | **no** | yes |
| Identity | in-memory, per session (§3) | persistent, device-bound (§3.1) |

A machine used often but not owned — a work laptop, a shared family PC — should become an
*enrolled* device by getting its own keystore, which needs no YubiKey: standalone password plus
recovery key is an existing tier. **There are two kinds of device, not three**, and the middle case
resolves by choosing one.

Full sync is not merely warned about for a temporary device session: the capability bit is absent,
so the request is refused at the protocol layer. **Capability should not be enforced by a warning
dialogue.** A warning is for things the user is allowed to do.

### Opening one archive on a machine that holds no keys

Exposure of *that archive's* data and its archive key is unavoidable — it has to be decrypted
there — but the rest of the keystore never leaves the phone.

Two rules make the blast radius that small:

- **The channel is append-only with respect to the keystore.** The PC may register a newly created
  archive and its key; it may not modify or delete existing entries, and may not touch slots.
- **Key release is scoped to the named archive.** One archive key is released, and it opens exactly
  that archive — the file index and every per-file DEK inside it, and nothing else.

Appending is also the cleanest possible sync primitive: a new archive has a fresh `archive_id`, so
it **can never conflict**.

**Withdrawal is possible here, unusually.** That machine received an archive key, so rotating that
archive's key afterwards cuts off its future access (`FORMAT.md` §7.3) — cheap, because rotation
re-wraps the per-file DEKs and rewrites the archive index without re-encrypting any data. As
everywhere else, it withdraws future access only; a copy the machine kept stays readable.

> Note this reverses an earlier decision *for this mode only*. `DESIGN.md` §10 rejected
> per-archive KWK scoping because a local manager should not re-prompt for every archive. Here
> the "prompt" is a tap on the phone and the counterparty is untrusted, so narrow scoping is
> exactly right. The two modes get different granularity on purpose.

#### Temporary device sessions

**§1's rule — a device needs its own keystore before it can sync — applies to enrolled devices
only.** A temporary session is the deliberate exception, and it is defined by what it does *not*
keep:

- **Received archive keys, and the decrypted archive index, live in memory and nowhere else**, and
  are zeroized when the session ends, the vault is closed, or the process exits.
- **Appended archives register in the phone's keystore**, since the machine has none.
- **The session's own keypair is generated at pairing and never written to disk** (§3).
- **No persistent pin.** Every session begins with a fresh pairing.

The last point is not a convenience trade, it is the whole safety of the mode. "Remember this
machine so it can skip the QR next time" would require persisting an identity private key on a
computer whose custody is not guaranteed — buying whoever sits down later a one-tap approval on
the owner's phone. **Strictly worse than scanning a code each time**, and no amount of hardware
binding fixes it (§3.1).

That also dissolves the "should a temporary pin expire by time or by use count?" question that
used to sit in §8: there is no such pin.

## 3. Device identity keys

Each keystore holds one long-term X25519 identity keypair, and the pinned public keys of its
peers. This is the SSH `known_hosts` model: trust on first use, then pin.

### Where the private key lives

**Randomly generated, wrapped under `KWK_identity`** — the same mechanism as an archive key, in a
different wrapping domain.

**Not derived from the VMK.** `identity_sk = HKDF(VMK, …)` looks tidy but the VMK rotates on
every slot removal, which would change the device's identity and invalidate every peer's pin.
Deleting one YubiKey would silently unpair every device. That alone rules the approach out.

**Not in the same wrapping domain as archive keys.** The low-trust channel's whole job is handing
out individual archive keys; if the identity key were wrapped under the same `KWK`, an over-broad
request or an indexing bug could dispense it. Separate domains make it structurally impossible.
The identity key must never be a member of the dispensable set, and that should be enforced by the
type system, not by a runtime check.

### Lifetime

**The identity key authenticates the handshake and nothing else. It never encrypts session
traffic.**

If sessions were keyed by static-static ECDH between the two identity keys, then anyone who
later compromised either private key could decrypt every recorded past session — and those
sessions carry bare archive keys. Ephemeral keys per session give forward secrecy, so a future
compromise cannot reach into the past.

```
identity key  →  proves "I am the device you pinned"
ephemeral key →  fresh per session, provides forward secrecy
```

In memory the identity private key is needed only for the moment of the handshake: unwrap, use,
zeroize. It lives in a `memguard` buffer like every other secret, and its window should be
milliseconds, not the session.

### Rotation and unpairing

**Unpairing is purely local** — delete the peer's pinned public key. No coordination, no
network, the other device need not be present. Rotating one's own identity key means re-pairing
with every peer, so it is a deliberate "this device may be compromised" action rather than
routine maintenance.

### 3.1 Device binding — making a copied keystore useless

An identity key wrapped only under `KWK_identity` travels with the keystore file: copy the file,
copy the identity. **Enrolled devices therefore bind the identity private key to the machine**, by
wrapping it a second time under a key the machine will not release elsewhere.

**No attestation is required, and asking for it would be a mistake.** Attestation proves *to a
remote third party* that a key lives in hardware, and needs an Endorsement Key, an attestation CA
and privacy-CA machinery. The counterparty here is the user's own phone, and the user has already
established which machine it is talking to by scanning a code off that machine's screen. The
property actually needed is narrower:

> **Someone who copies the keystore cannot reuse the identity.**

A non-exportable key delivers exactly that, with none of the infrastructure.

**TPM, where available.** Create the key under the storage primary with `TPM2_Create`, keep the
returned `TPM2B_PRIVATE` blob and public area **in the keystore file**, and `TPM2_Load` it
transiently to use it. The blob is loadable only by that TPM.

*This consumes no persistent TPM storage.* Persistent handles are scarce — single digits to low
tens — and leaving an orphaned key in a machine's TPM would be poor hygiene with no way to clean
up. The wrapped-blob model avoids the question entirely: the key material lives in our file,
deleting the keystore deletes it, and nothing is left behind on the machine.

**DPAPI otherwise**, and **machine scope specifically**. User-scope DPAPI keeps its master key in
`%APPDATA%\Microsoft\Protect\<SID>\` — inside the user profile, so a profile backup or a synced
user folder carries it along and the protection evaporates against exactly the threat it was
chosen for. Machine scope keeps it in `%WINDIR%\System32\`, outside anything a user-folder backup
touches. The cost — any process on the machine can unwrap it — is already covered by the phone
approving every request individually.

#### What each tier is actually worth

| Threat | Unbound | DPAPI | TPM |
| --- | --- | --- | --- |
| Keystore copied to another machine | ✗ | ✓ | ✓ |
| User storage synced or backed up | ✗ | ✓ | ✓ |
| Full disk image, analysed offline | ✗ | ✗ | ✓ |
| Malware running as the user, live | ✗ | ✗ | ✗ |
| Malicious kernel | ✗ | ✗ | ✗ |

Two conclusions follow, and both are load-bearing:

**TPM's only advantage over DPAPI is the offline disk image.** Neither stops live malware — a TPM
prevents a key from being *extracted*, not from being *used*, and malware on the machine simply
asks the TPM to use it. The large step is unbound → DPAPI; TPM → DPAPI is a small one.

**DPAPI is always available on Windows**, so the "unbound" column never occurs there. It is a
placeholder for platforms added later. On Windows the tier is therefore **informational**: show it
once at pairing so the user knows how strongly the device is bound, record it in the pin record so
it can be reviewed later, and **gate nothing on it**. The gate that matters — persistent pin or not
— is decided by whether the machine has a keystore (§2), which lands on the same line anyway.

#### Two hard boundaries

**Device binding never appears on the unlock path.** A TPM is lost when it is cleared, when the
firmware is reset, when the board is replaced, when Windows is reinstalled. If any of that could
brick the vault, the design would be indefensible — nobody expects a motherboard swap to destroy
their archives. On the sync path the worst case is *re-pair*, which is recoverable and
comprehensible. Keep it there.

**Temporary sessions do not use device binding at all.** Their keypair is generated at pairing,
held only in memory, and destroyed with the session — there is nothing persistent to bind, and
nothing left on the machine afterwards. The key exists so that a dropped connection can be resumed
within the session (§4.1); it is never pinned beyond it.

## 4. Handshake

Two cases, distinguished only by whether the peer's public key is already pinned. Each side
decides independently from its own pin list; the two sides disagreeing is fine and correct.

| | First contact | Already pinned |
| --- | --- | --- |
| Establishing | QR carries the displayer's X25519 public key **and a fresh 128-bit pairing secret**, used as a Noise **PSK**; or a short screen code driving a **PAKE** (CPace / SPAKE2) when no camera is available | **Noise KK** — both sides already know the other's static key |
| Properties | Confidentiality and freshness from the out-of-band channel; **completed by a mutual confirmation**, then both pin | Mutual auth, forward secrecy, 1-RTT |
| User does | Confirm a matching value on both screens, then a strong warning, 5–10 s enforced delay, biometric | One confirmation tap |

An X25519 public key is 32 bytes and a pairing secret another 16, so the whole payload fits in a
QR trivially. The PAKE branch exists only for the fallback where there is no camera and the user
reads a short code aloud or types it.

### Why the QR needs a secret and not just a public key

An earlier draft put only the public key in the QR and called the result mutual authentication. It
is not. The key in question is the keystore's **long-term identity public key** (§3) — every
device that has ever paired with this one already has it, and it is not secret in any useful
sense. A QR containing only that conveys nothing an old peer does not already hold, so any of them
could open a handshake at any time and be accepted as a brand-new device.

A **fresh random secret per pairing, mixed in as the Noise PSK**, is what makes the QR mean
something: only a party that saw *this* QR can complete *this* pairing. CTAP hybrid does exactly
this, and it costs 16 bytes.

### Why a mutual confirmation as well

The PSK does not close everything. Anyone who can see the screen — a photograph, a remote
screenshot from malware, a camera behind the user — reads the secret too, and could race the real
phone to complete the pairing. That residual is the same one CTAP hybrid has, and CTAP answers it
with a BLE proximity proof, which is unavailable here (Bluetooth permission on a phone is a
tracking capability users reasonably refuse, and not every PC has a radio).

So first contact ends with **both devices displaying a value derived from the completed handshake,
and both users confirming it matches** — the Bluetooth numeric-comparison pattern. An attacker who
raced the pairing shows a value the user's own phone is not showing.

This does not contradict the "no short authentication string" position below; it sharpens it.
**That argument is about frequency.** A comparison performed on every login becomes routine and
stops being read; one performed once in a device's lifetime, while the user is actively waiting
for the phone they just used to scan a code, is a different act entirely.

Make it hard to pass reflexively: instead of "do these match? yes / no", show **three candidate
values on the phone and have the user pick the one on the PC's screen**. Tapping *yes* without
looking is easy; picking correctly from three without looking is not.

> **The full fix for the screen-observer residual is a bidirectional QR** — the PC reading a
> response from the phone's screen with its webcam, which would require an attacker to make the
> user's PC read *their* phone. It is listed as a transport option in §8 and would remove the need
> for the comparison step entirely.

**The public key never goes to a relay.** It exists on the screen and nowhere else. A relay
therefore cannot substitute its own key, because it never handles one.

Corollary, taken from CTAP: **derive any rendezvous identifier from a secret the relay cannot
learn, and specifically not from the proximity secret.** CTAP derives its tunnel ID from the QR
secret alone rather than from the BLE nonce, explicitly so the tunnel operator cannot brute-force
the nonce out of the identifier it was handed. Hand a relay an identifier that is useless for
reconstructing anything.

### No *routine* short authentication string

**A comparison step that recurs — on every unlock, every session, every reconnect — is
deliberately not part of this design.** The once-per-lifetime pairing confirmation above is the
exception, and the distinction is frequency, not principle.

For an already-pinned peer, Noise KK authenticates both sides from keys they already hold, so
there is nothing for a comparison to catch and asking for one every time would only train the user
to dismiss it. A PAKE is likewise secure by construction against an active MITM holding only a
low-entropy secret, giving the attacker one online guess per attempt.

Telegram and Signal need an SAS because their two parties are *remote* and no out-of-band channel
exists. Enfold's two devices are in the same room, which is exactly the condition that permits a
real authenticated channel instead.

Adding one anyway has a cost: **users do not compare.** Bitwarden's login-with-device rests
entirely on a user comparing five words, and Bitwarden itself names that as the defence against a
tampered server. The WiSec 2025 analysis of WhatsApp linking makes the constructive version of
the point — the fix is to make the short code *key material* rather than a *compared value*,
"even if an attacker captures the SAS by eavesdropping it, it would not help the attacker as the
SAS does not contain secret key material."

This is the same principle as CTAP folding the BLE advertisement into its PSK rather than
checking it:

> **A compared value is a policy check — skippable, and humans skip it.
> A value that is a key input makes the attack impossible — there is nothing to skip.**

Put the entropy into the key derivation, not onto the user's eyeballs.

### What to show instead

Consequences, not checksums. Users read the former.

```
✗   🐧🍎🚗🌙🔑              nothing to compare it against
✓   Pair with "DESKTOP-ABC"?
    It will be able to:      open archives, add file keys
    It will NOT be able to:  receive the full keystore
```

### Prompts are bidirectional and asymmetric

Prompt whenever the peer is **not in your own pin list** — a purely local test needing no
coordination.

The two sides are approving different things, and one of them carries the risk:

| | Shows | |
| --- | --- | --- |
| **Phone** | "DESKTOP-ABC requests ⟨the full keystore / keys for archive *Work*⟩" | **Releases the secrets.** The warning, the 5–10 s delay and the biometric check belong here |
| **PC** | "Pair with phone *Pixel-9*? It will be able to…" | Accepts an identity to pin |

The delay and biometric must run **on the phone**, because the PC is the untrusted party in this
relationship. A confirmation rendered by the device you are trying to protect yourself from
proves nothing.

Matching is on the pinned public key. `device_name` is displayed and never matched — otherwise
an attacker names their device "My Pixel" and survives a glance.

## 4.1 Sessions, resumption, and the approval-skip window

Every key release requires an approval on the phone. The **approval-skip window** — "don't ask
again for the next N minutes" — is the one exception, and getting its scope right is what makes a
stolen device identity survivable.

### The grant belongs to the session, never to the device

> An attacker holding the device identity key can open a **new** session. A new session carries **no
> grant**, so every request prompts — on a phone whose owner did not initiate them.

This is the single most important rule here, and it is why the identity key being stealable is not
fatal. The identity establishes *connectivity*; authority is something the user granted inside one
specific session, by looking at their phone. It is not an attribute the device carries around.

A consequence worth stating: **the device-binding tier (§3.1) does not gate this window.** Binding
strength is irrelevant to a grant that a stolen identity cannot inherit in the first place.

### Resume skips the human, never the cryptography

Networks drop. IPv6 privacy extensions rotate addresses on a timer, CGNAT changes the apparent
source, phones move between cellular and Wi-Fi. **A session must therefore be a cryptographic
object, not a network one — never bind one to an IP address.**

```
resume  =  full Noise KK handshake with the pinned static keys
        +  proof of possession of the session's resumption secret
        −  the human confirmation
```

**The resumption secret is not a bearer token.** Requiring the handshake *as well* means an
attacker needs both the identity key and the session secret; the secret alone, or the identity
alone, gets nothing. The fresh handshake also produces fresh ephemerals, so **forward secrecy is
renewed on every reconnect** rather than a stale key being extended.

The resumption secret lives **in memory only** and is never written to disk. That is what turns the
attack window from *any time later* into *concurrently* — a file persists across reboots, gets
backed up, and sits waiting; memory does not.

**Ratchet it.** Each resume derives the next resumption secret from the current one. A captured
secret is then single-use, and a replay is *detectable*: the legitimate endpoint's next resume
fails, which is a signal rather than a silence. One HKDF per reconnect.

### A new session revokes the old grant

Opening a new session to a peer that already holds a live grant **ends that grant** and notifies
the phone: *a new session started on DESKTOP-X; the approval window was closed.*

So an attacker's new session both lacks the grant and destroys the victim's — loud rather than
silent. They can repeat this to deny the user their window, which is irritating, highly visible,
and not a disclosure.

### Window durations and when the window ends

| | Enrolled device | Temporary device session |
| --- | --- | --- |
| Maximum window | 60 minutes | 15 minutes |
| Pre-selected in the UI | may be | **never — the user must actively choose it** |

The window ends immediately on any of: expiry, a new session to the same peer, the session closing
(application exit or workstation lock), or an explicit revoke from the phone.

**Deliberately not tied to the phone locking.** People lock their phones constantly; a 60-minute
window that died on every lock would last seconds, and the feature would be disabled by users
rather than used carefully.

### The two conveniences must not compose

A persistent pin (skipping the pairing ceremony) is safe on its own. An approval-skip window
(skipping per-request approval) is bounded on its own. **Together they are an unattended grant**,
and someone who sits down at a still-running session inherits both.

> On a temporary device session, the approval-skip must be chosen by the user **inside that
> session**, and may never be restored from any saved preference.

Enforce this in the protocol rather than trusting a settings toggle. The danger is not either
convenience; it is their product.

## 5. Merge

Sync reconciles metadata and archive keys only. Slots are out of scope by §1, removing the one case
that genuinely could not be merged.

### The primitive is a revision counter, not a timestamp

Timestamps cannot distinguish the two cases that matter:

```
A is ahead, B is a stale replica   → safe, take A
A and B were each edited apart     → real conflict, the user must see it
```

Clock skew between a phone and a PC is ordinary, and users change clocks. Last-write-wins by
timestamp is the classic silent-data-loss bug. `modified_at` is kept for display and as a final
tie-break only.

`(revision, last_writer)` on each record answers the question a hash cannot: a hash says
*different*, not *descended from*.

### Union by default

**What is being merged is archive records, not file records.** File records live inside archive
files, not in the keystore, so they never take part in keystore sync at all. Each side's registry
is a set of `archive_id → {name, policy, versions, …}`.

For a keystore the safe default on conflict is to keep everything, because nothing is destroyed by
doing so.

| Situation | Resolution |
| --- | --- |
| Each side registered different archives | Union. **Not a conflict at all** |
| One side ahead on an archive record, other stale | Take the higher `revision` |
| Both renamed or re-policied the same `archive_id` | Surface it and let the user choose. Metadata, so nothing is lost either way |
| Version lists differ | **Union the versions.** A key one side has and the other lacks opens some copy that exists somewhere; dropping it orphans that copy |
| One side deleted, other edited | Keep the edit, surface it. Deletion never wins silently |

Deletions propagate as **tombstones**, not as absent records — otherwise a union resurrects
everything the peer deleted. Tombstones are garbage-collected only after every enrolled peer has
acknowledged a sync point past them.

**Version lists union rather than taking a winner**, which is worth stating separately because the
instinct is to treat a longer list as newer. It is not a version history in the ordinary sense: it
is the set of keys that open copies of this archive that may exist anywhere in the world. Losing
one loses access to a backup, so the union is the only safe rule.

**Divergent archive *files* are a different problem and out of scope here.** If the same archive is
edited on two devices, two files exist with the same `archive_id` and different contents. Keystore
sync reconciles keys and metadata; it does not merge archive contents, and it should not pretend
to. See §8.

### Archive key handling

For each archive to transfer: unwrap its archive key under the sender's `KWK`, send it inside the
session, re-wrap under the receiver's `KWK`. The key value is unchanged; only the wrapper differs.

**Every version record travels, not just the current one** (`FORMAT.md` §7.2). A peer that
receives only the current key cannot open a copy of the archive made before the last rotation, and
the whole point of retaining retired keys is that such copies stay openable.

`last_ciphertext_hash` travels too, so the receiver can tell whether an archive file it finds on
disk is the current one without unlocking anything.

## 6. What a relay can see

If a relay is ever used (LAN-direct and QR-both-ways should be tried first, since the channel
carries only kilobytes of key material — bandwidth is irrelevant here):

- **Cannot see:** identity public keys, archive keys, metadata, filenames, archive names. Everything is
  end-to-end encrypted inside the session, and the public keys never reach it.
- **Can see:** timing, message sizes, IP addresses, and the pairing graph over time.

Mitigations: pad messages to a fixed granularity so sizes leak nothing, and keep channels
short-lived — CTAP defines no QR expiry at all and Chromium does not even read the optional
timestamp field, so an explicit 60–120 s window has to be enforced by the implementation.

Endpoints should be **capability URLs**: a path derived from high-entropy material the attacker
cannot know, so an attacker cannot even name a valid channel. No accounts, no certificates —
a certificate embedded in a distributed binary is extractable and endorses whoever extracts it.

## 7. Abuse cases this design is built against

Device-linking QR flows have been abused at scale against the two most security-conscious
messengers, and this is the same shape of flow:

- **Signal, February 2025.** UNC5792 cloned legitimate group-invite pages and replaced the
  `sgnl://signal.group/…` link with `sgnl://linkdevice?…`, so the victim's own app performed the
  linking. Once linked, messages flowed to the attacker in real time with no device compromise.
- **WhatsApp, January 2025.** Star Blizzard sent a deliberately broken QR first to elicit a
  reply, then a working link to a page hosting a real device-linking QR.

The transferable lesson: **users are trained to scan QR codes and approve prompts, and a remote
attacker's QR is visually indistinguishable from a local one.**

Enfold's exposure is at pairing. The mitigations are already above, listed here so they are not
optimised away later as friction:

1. The strong warning, the enforced 5–10 s delay and the biometric check on the phone side.
2. The prompt states the *consequence* ("will be able to receive the full keystore"), not just an
   identity.
3. A QR carrying a **fresh per-pairing secret used as a key input**, not merely a public key: a
   remote attacker's request cannot produce the secret shown on the user's own screen, so a
   remotely-solicited approval cannot complete. Someone who can *see* the screen still can — hence
   the next point.
4. A **mutual confirmation** ending first contact, presented as a pick-one-of-three rather than a
   yes/no, so an attacker who raced the pairing shows a value the user's own phone is not showing.
5. Ephemeral devices cannot receive a full sync at all — the capability does not exist for them,
   so the most damaging outcome is unreachable by any amount of social engineering.

## 8. Open

**The first-contact transport is deliberately deferred until after the desktop version ships.**
Choosing a relay means hardcoding a hostname into distributed binaries, which cannot later be
changed without maintaining two of everything. That commitment should not be made before it has
to be, and three things reduce the chance it ever has to be:

- **Desktop-only v1 needs no hostname at all** — no sync, no pairing, no relay.
- **LAN mDNS and QR-both-ways need no hostname either**, so the phone version can ship without
  one.
- If a relay does become necessary, **hardcode a name, not an endpoint**: bake in one immutable
  URL under a controlled domain, serve a signed manifest there naming the current relay, and let
  the infrastructure move behind it. This is the same pattern as the update channel, and it turns
  "can never be changed" into "change a signed file". Cost is one extra request.

Still open:

- Which of LAN mDNS, QR-both-ways via webcam, or a relay to build first. All three are viable
  given how little data crosses; the choice is UX, not bandwidth.
- **Divergent archive files.** If one archive is edited on two devices, two files carry the same
  `archive_id` with different contents. Keystore sync handles keys and metadata and deliberately
  does not touch this. Options range from detecting it via `last_ciphertext_hash` and telling the
  user, through to a full per-file merge inside the archive — the first is almost certainly right
  for v1, but the decision has not been made.
- Tombstone GC horizon when a peer has not synced for a long time.
- Whether `SetProcessMitigationPolicy` with `ProcessSignaturePolicy` (Microsoft-signed DLLs only)
  is worth enabling. It blocks a class of injection, but can conflict with anti-malware products
  and shell extensions, so it needs testing rather than a decision on paper. Note that it is
  hardening, not a boundary — see `DESIGN.md` §2 on same-user isolation.

Two items previously listed here are now closed. **Whether a temporary device's pin should expire
by timer or use count** — there is no such pin (§2). **Whether resuming a pinned session needs a
fresh proximity proof** — resumption requires a full handshake plus the session's memory-only
resumption secret (§4.1), and a *new* session gets no grant regardless, so the proximity question
does not arise.

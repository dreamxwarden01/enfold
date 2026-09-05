# Enfold

A compression and archive manager whose distinguishing feature is security and privacy.
Windows first, written in Go.

**Status: design settled; implementation in progress, bottom up.** This repository holds the
specification, the reasoning behind it, and the first two layers of code: `internal/format`
encodes and decodes both file types byte-for-byte per `docs/FORMAT.md`, with fuzz targets for
every decoder; `internal/kdf` implements the whole key hierarchy of `docs/FORMAT.md` §3 and
reproduces every value in `testdata/kdf-vectors.json`, which `tools/kdfvec` generated and an
independent implementation written from the spec alone confirmed. Run tests with
`scripts/test.ps1` (the full suite includes a 512 MiB Argon2id anchor; `-short` skips it).

## The shape of it

One **keystore file** holds the unlock methods and the keys to every archive. **Archive files** are
separate and portable — they are meant to live in places you do not control, so nothing that could
unlock one is ever written into one.

```
keystore                                    archive files (anywhere)
  slots + encrypted registry                  envelope + encrypted index + data

VMK ── KWK ── archive key ── per-file DEK ── file content
```

Unlocking the keystore is backed by a YubiKey (PIV slot 9d, P-256 ECDH), so the key-encryption key
never exists on disk. An optional passphrase can be entangled into the derivation, making an
extracted hardware key insufficient on its own. A slot invariant enforces that **two independent
ways in always exist** — defined as two slots whose required-secret sets are disjoint, not merely
as a count.

## Documents

| | |
| --- | --- |
| [`docs/SCOPE.md`](docs/SCOPE.md) | **Start here.** What v1 ships, what it does not, and what must happen before the format is frozen |
| [`docs/DESIGN.md`](docs/DESIGN.md) | Threat model, key hierarchy, slots, session model, and a checklist of traps |
| [`docs/FORMAT.md`](docs/FORMAT.md) | Byte-level layouts, AAD definitions, algorithm registry, rotation procedure |
| [`docs/SYNC.md`](docs/SYNC.md) | Device pairing, identity binding, session semantics, merge rules |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | Dated log of every decision, its reasoning, and what was rejected |

`DECISIONS.md` is append-only and keeps reversals rather than editing them away. Several entries
record where an earlier conclusion turned out to be wrong and why — that history is the point.

## Notes on the threat model

The design is explicit about what it does not do. It does not defend against malware running as
you while the vault is unlocked, and it cannot: Windows provides no process isolation between
programs of the same user, and none can be built in user mode. It also carries a known
post-quantum limitation — the software slots are hybrid X25519 + ML-KEM-1024, but the hardware
slot cannot be, because no shipping YubiKey performs ML-KEM.

Both are stated plainly in `DESIGN.md` rather than glossed over.

# VMK rotation at a removal and a password change, and replacing the recovery keys: the design

The designer's document for the feature the owner ruled on 2026-10-27, "build it"
(docs/DECISIONS.md:5245-5257). **Revision 5, the final**, written after two critiques of revision
4 (scratchpad/vmk-rotation/critique3-format.md and critique3-ux.md; section 7.7 answers them).
Revision 4 followed the outside review's reading of revision 3 whole (astra_vmk_design_review2.txt
in the session's scratchpad, with 44 files; section 7.6 answers it) and the owner's rulings on that
review of 2026-10-27 (R-A to R-D below). Revision 3 followed two critiques of revision 2
(critique2-format.md and critique2-ux.md), which followed the first outside review
(astra_vmk_design_review.txt) and the owner's five rulings on it. Revision 4 is kept as
scratchpad/vmk-rotation/design-rev4.md, revision 3 as design-rev3.md, revision 2 as
design-rev2.md, revision 1 as design.md and the draft before it as design-rev1.md. Read-only work:
nothing was built, run or edited in the repo but this file.

**The rulings of 2026-10-27 on the first outside review** (they bind, and sections 2 to 5
describe them):

1. The backup an act takes is written **after** the change and its retirement, at the new key.
   This reverses "taken before" (2026-09-07, DESIGN.md:1001-1003; 2026-10-17, APP.md:3845-3846);
   section 4 records the reversal in FORMAT §18.3, APP.md §13 and DECISIONS.
2. Replacing the recovery keys is **not** folded into a removal or a password change. It is a
   separate, atomic act on the Vault page, *Replace recovery keys…*: one ceremony and one
   transaction that makes and shows the new keys (under "the existing showing rules: save as a
   text file, print, or written down with a second confirmation, ruled 2026-09-07" — words of a
   ruling 2026-09-09 superseded, so R-B below holds the later one, with no second confirmation,
   for the replacement too), removes every old recovery key and re-keys the vault, so that no
   new key is ever escrowed under a state an exposed recovery key still opens. The removal's
   "lost" path and the password change's "someone else may know it" path point to it, in their
   info line and in their end, saying when it is needed (an earlier copy of the vault file may
   be out there) and, with the password on, to change the password too.
3. The 2026-10-17 ruling "the dialogs offer no recovery key" (strings.ts:168-169) is reopened,
   narrowly: where no YubiKey can serve the proof — the vault's only YubiKey is the one being
   removed as lost, or the user says at *Add an unlock method…* that the YubiKey is lost — the
   proof may be a recovery key. The real case behind the feature, "my only YubiKey is lost and
   the vault has one recovery key", must work end to end on screen (3.4 walks it). The reveal's
   rule "never through the recovery key itself" (DESIGN.md:344-346) still governs the showing of
   an escrowed recovery key, and nothing else.
4. Retirement is not best effort: a retiring commit that fails leaves a durable, recoverable
   "changed but not retired" state, finished at the next unlock before the Session is installed
   and before any vault write is admitted; the act reports what happened (2.2.3). What holds
   while it cannot be finished is R-A's (2.4).
5. The outside review's findings 5 to 8 are taken as written (section 7).

**The rulings of 2026-10-27 on the second outside review** (they bind too; section 7.6 takes the
review point by point):

- **R-A.** A retirement that cannot be finished leaves the vault **readable**. The barrier holds
  every keystore and registry mutation — a slot change, a password change, a replacement, a re-key
  of the vault, creating an archive, re-keying an archive, forgetting or deleting a record, any
  save that writes a new secret or a new record into the registry, a backup export — until the
  retirement has landed in full, every pass a legacy tail needs included, and such a mutation
  first tries to finish it. **Archive content writes are not held**: adding, replacing, renaming,
  moving or deleting files inside an existing archive changes no key and publishes nothing into
  the registry, and an archive's key is unchanged by a VMK rotation, so content written while a
  retirement is owed is exposed exactly as content written after it. The designer verifies that
  claim against the code, every operation and what it writes into the registry, and gates exactly
  those that write a record or a secret, by name (2.4 does). The gate's state survives Locked and
  Broken as nonsecret facts. Unknown outcomes stay Broken.
- **R-B.** The ruling of 2026-09-09 holds everywhere: no second confirmation for any way a
  recovery key is kept, in the replacement too, which keeps its one confirmation before the
  commit, the list pairing each new key with the one it replaces. SCOPE.md:41-43 and
  DESIGN.md:348-351 are brought to 2026-09-09's words (4.2, 4.3).
- **R-C.** The review's findings 1, 3, 4, 6, 7, 8, 9, 10 and 11 are taken as written. Finding 1: a
  durable, crash-safe cleanup of obsolete registry ciphertext that never rewrites a live sector,
  with the owed condition extended so that an interrupted cleanup is finished, and the claim that
  such ciphertext is inert removed everywhere (2.2.3, R41). Finding 4: a vault that is Tampered and
  owes a retirement stays readable, and every mutation of R-A is refused until an import or a repair
  establishes a clean state, the repair being the retirement's own Tampered branch, which copies
  nothing forward; R25 still forbids copying the damaged region forward (2.2.3, 2.4). Finding 8: the
  act's backup runs only after a completed retirement, and is otherwise reported as skipped for that
  reason, its stamp unchanged (2.3).
- **R-D.** Kept: one commit for each change and its rotation, and for the replacement; the full
  retiring commit; the backup after; the acceptance walkthrough; everything else revision 3
  settled.

**Anchors.** HEAD is b79a10c; the tree is clean but for this file. Every file:line below was read
at b79a10c by this revision's author, not taken from the maps.

**Words.** On screen an *unlock method* (never "slot", and never "way in": the glossary forbids
both, frontend/src/lib/strings.ts:12-14), *re-key the vault* (never "rotate" or "VMK"), a
*recovery key*, and a YubiKey, never a bare "key" (strings.ts:9-31, strings.test.ts:175-183,
198-212). This document, like APP.md, keeps the spec's own terms in prose (APP.md:3428-3429).

**What changed from revision 4** (section 7.7 takes the two critiques finding by finding; where the
lists below for earlier revisions say otherwise, this one stands): the handle keeps the file's
physical end, raised before every registry write, so that the padding, the trim, the zero-fill
and the predicate reach a registry that a commit which failed before its flip left past the end
of the last flip (2.2.3, rule 8); a commit that fails before its flip is retired at once inside
its own act, so a replacement's never-added keys and a failed enrolment's record do not wait in
the file for a later mutation (2.4); on a Tampered region the retirement does everything that
copies nothing forward — a registry-only commit in place of each retiring one, zeros over the
slot region copy no superblock copy names, the zero-fill — and refuses only a live copy's own
tail, which the import cures (2.2.3, 2.4); the import's confirmation says that the dated copy it
keeps of a vault that still owes a retirement opens with what the owed change withdrew (2.4, 4.1,
R41); the barrier never reads a handle a ceremony holds, and *Import archives from a backup…*
meets it at its inspection, before its proof (2.4); a failure after the commit landed ends the
act Done (2.4); the retirement's words have one cause sentence true in every case and the full
list of what waits, the act's skipped backup said in that bar rather than in one of its own, and
the bar says less where only earlier registry ciphertext is left (3.6, 3.7); the recovery-key
switch also serves adding a recovery key, so that a lost YubiKey can be withdrawn before a spare
is bought (2.3, 3.4); the switch is one tested predicate; the chain's facts reach the
replacement's dialog, and a vault left with recovery keys alone is always pointed to an unlock
method first (3.5); a one-key replacement reads singular everywhere; the keep mode is not
dismissed by Escape (3.3); the exported test seams the app's tests need are named (2.6); DESIGN's
TRIM paragraph and its neighbours, and SCOPE's rotation line, agree with the rest (4.2, 4.3); the
section 4 deltas are wrapped at 96.

**What changed from revision 3** (section 7.6 takes the review position by position and finding
by finding; where the lists below for earlier revisions say otherwise, this one stands): a fourth
clause of the owed condition, a nonzero byte anywhere in the registry area outside the two
registries the superblock copies name, met by a scrub that every commit makes after its flip — it
writes zeros over whole 4 KiB units no superblock names, so never a sector a reader reads — and by
every registry write padding its last unit with zeros; leftover registry ciphertext is no longer
called inert (2.2.3, R41). The barrier replaces revision 3's archive gate: it holds the keystore
and registry mutations R-A names, each first finishing the retirement in full, and never the
content of an existing archive, which the table of 2.4 verifies writes no record and no secret;
its state is two cached facts that survive Locked and Broken; a Tampered vault that owes is
refused those mutations rather than let through (2.4). The act's backup is skipped while a
retirement is owed (2.3). The replacement's keep mode asks no second confirmation, and SCOPE and
DESIGN say 2026-09-09's words (3.3, 4.2, 4.3). A replacement whose outcome is unknown, a conflict
included, never tells the user to destroy the keys shown (3.3). The end's next step reads the
facts the act committed, carried in its final state, and what keeps it from pointing back to new
keys is scoped to a chain of acts begun from one another's ends (3.5, 3.6). APP.md's *No match*
rule names the enrolment's exception, and the password's disclosure says there is no field for
the old password while a YubiKey's proof still asks the current one (3.2, 4.1). Section 6 has no
open question; the tests the review named are added (2.6, 3.9).

**What changed from revision 1**, in one list: the backup moves after the change unconditionally
(Q1 is answered); *Replace recovery keys…* is designed (2.7, 3.3); the recovery-key proof is
reopened for two cases and the lost-YubiKey walk-through is the acceptance scenario (2.3, 3.4);
retirement is made durable by a predicate over the file's own bytes, carried by every commit and
finished at unlock (2.2.3); the dialogs' bodies stop promising that no YubiKey is needed (3.2);
every unconditional "reaches no archive created from now on" is qualified by what the recovery
keys' escrow still reaches (3.1, 3.2, 4.3); the mock plays the *Turn on…* ceremony (3.10).

**What changed from revision 2** (section 7.5 takes the two critiques finding by finding): the
retirement's predicate also reads the losing superblock copy, and a retiring commit never writes
the live slot region copy in place, so a file with an old tail takes two of them (2.2.3); the
claim that a second body gives the reader a fallback is struck, since FORMAT has none (2.2.3,
R41); archive writes wait at admission while a failed retirement is owed (2.4); a commit marks
the handle owed as soon as it writes a region copy; a password removal keeps proving with the
password; every act that re-keys ticks its backup; the end's pointers form one chain, the
password first, with *Add an unlock method…* where the vault is left with recovery keys alone,
and never a loop back to a replacement just made (3.5, 3.6); the replacement's end no longer
tells the user to destroy earlier backups, which FORMAT §18.2 keeps for merges; *Finish setting
up* is refused before its ceremony while the vault's password is on (2.3); the keep mode asks a
second confirmation for *I have written it down*, as ruling 2's words say, and section 6 asks the
owner to confirm it; the copy, the spec deltas and the tests follow.

---

## As built (2026-10-28)

Built on 2026-10-28 from this revision in six parts, each built and reviewed, and repaired where
its review asked (A1, A2, B, C, D1, D2, with an integration review and repair of A1, A2 and B
between; the notes are in the session's scratchpad, vmk-build/), and part E brought APP, SCOPE,
DESIGN, FORMAT and DECISIONS to the code. The whole feature lands as one git commit on top of
fe9b2ec. **From now on the specs, not this document, are the authority**: where the two differ,
the code and the specs are right.

*The commits' shape.* An act that writes the slot region makes one commit, its change and any
rotation together, then `Retire`: one retiring commit, two for a live copy's legacy tail, a
third never (corrupt), each flip followed by its zero-fill; a commit that fails before its flip
is retired at once inside its act; a registry-only save carries one retiring pass while a region
copy or the losing superblock copy is owed and the region verifies, plainly when it cannot.

Where the build departed from the design, and why:

- A (keystore). `load` decodes the losing copy itself, so `PickKeystoreSuperblock` is unchanged;
  clause 3 also counts another vault's losing copy and a registry extent outside the file; a
  byte that cannot be read counts as owed and the file still opens (2.2.3 was silent; R-A wants
  a readable vault); a scrub-only `Retire` takes the size from the file; `zeroOther` checks the
  disk first, as the scrub does; a prepared recovery slot is bound to its vault, and its ID is
  drawn at most 16 times; a live recovery slot with no escrow record still rotates.
- B (app). A `Retire` error with nothing owed counts as landed; `noteFileLocked` refreshes the
  date and the size, not only `retireOwed`, after every core-made write; a Busy reopen keeps
  `retireFailed` (`busyID`); `beginAs` refuses every act, the reveal included, during a Delete's
  claim; a lock between a recovery key's enrolment and its showing ends the act Done; every
  backup that would hold no recovery key is refused before the proof, the export's too. A
  Tampered vault's export stays refused: 2.4's "admitted as today" was a slip.
- B, after the outside review of the diff (findings 3-8). A new archive's key, and an archive
  re-key's, is wrapped inside the held registry write, under the Session it commits with; a
  re-key drops the merge handles, and a merge refuses one wrapped at another generation; the
  barrier refuses while a ceremony or a pending touch holds the handle (2.4, step 1), and the
  registry write checks that before it reads the Session; a write that lands with the handle
  Broken after it, and a Broken handle met at the Session, enter Broken; *Finish setting up*
  retires a first way in that failed after writing, and one that landed before a cancel or a
  lock trigger, which then ends Done; a replacement's new recovery key leaves ordinary memory
  before its keep prompt. After the review of those repairs, an archive re-key meets the barrier
  before anything reads the vault, its forgotten check in the same hold (2.4).
- C (bindings, mock). The bindings come from build/Taskfile.yml line 62, with `-f
  '{{.BUILD_FLAGS}}'` (section 5 says line 61, without it; the output is the same); the mock
  omits empty fields as the core does, and its `retire_owed` knob also takes "owed" and "lands".
- D (page). A recovery key's enrolment ends at `StepRecovery`, not at Done (save when a
  lock landed before the recovery key could be shown), so its "add" next step (3.6 item 4)
  stands in the Vault page's notice slot once that dialog closes; an enrolment's commit
  leads "Adding the unlock method…"; every line that counts recovery keys has its singular,
  3.4's one-key walk reading "the recovery key"; the *How this works* texts and the
  command keep their plural; the lock screen's line under a greyed *Finish setting up…* is
  its reason; the Re-key dialog is not a form; a backup that cannot be made draws no reason.

Left: section 6's follow-ups, which DECISIONS' entry of 2026-10-27/28 lists under *Not done
here*, and the nits it lists under *Seen in the build and left*.

---

## 1. What is built today and what is missing

Built:

- Keystore `RemoveSlot` (internal/keystore/mutate.go:565-586): one commit at the same
  generation; the invariant over the remaining set (580-582); `commitSlots` (761-773) prunes the
  removed recovery slot's escrow record (`pruneSecrets`, 862-880). Its comment already says "the
  caller offers Rotate, with rotation pre-selected" (562-564).
- Keystore `Rotate` (mutate.go:597-738): FORMAT §8 entire, in one commit with the header copied
  forward (730), the new VMK and generation installed on the handle (735-736). It prunes against
  the CURRENT slot set (665) and re-wraps every active slot of the current set (714-723), each
  from its stored public keys alone (`rewrap`, 744-754; `wrapHybrid`, slots.go:309-324, reads
  only `SlotPubkey`, `MLKEMEK` and `RecipientID`).
- Keystore `ChangeEntangledPassword` (mutate.go:347-364) and `SetEntangled` (310-339), both
  through `writeEntangle` (373-408): a fresh salt and K_P, hardware slots re-wrapped at the SAME
  generation, one commit, `installKP`.
- Keystore `AddSlot` (mutate.go:173-227): a recovery slot's escrow record is written in the same
  commit as the slot (215-226); `newRecord` (slots.go:215-279) draws the recipient ID and the
  slot salt, derives the public keys and wraps the VMK at the handle's generation.
- One `txn` carries slots, header, registry and generation together (file.go:275-291); the
  superblock write is the commit point (file.go:413-423).
- The commit leaves the losing superblock copy naming the previous state ON PURPOSE: the region
  goes to the inactive copy (file.go:355-356), the superblock to the other one (416), and the trim
  keeps the losing copy's registry addressable "so that a later loss of the live copy opens the
  file one commit behind rather than not at all" (425-429; FORMAT.md:879-882). The archives
  already retire their losing copy with a follow-up commit where it matters (R31,
  FORMAT.md:552-569).
- The proof of a slot change, `acquireUnlocked` (internal/app/keys.go:249-339): a YubiKey whenever
  a hardware slot is enrolled and the card layer exists (285), else the password (325), and the
  recovery key only when the vault has neither (268-283).
- App: `Core.RemoveSlot` (keys.go:849-864, kind "remove"), `Core.RotateNow` and `rotateWith`
  (867-903, kind "rotate", the Session re-derived), `Core.ChangeEntangledPassword` (109-137) and
  `Core.SetEntangled` (66-107), both kind "entangle"; each its own ceremony through
  `beginMutation` (181-183) and `beginWithVMK` (190-233). `BeginEnroll(recovery)` commits the new
  slot first and shows it after (keys.go:379-401): the ceremony ends at `StepRecovery` with the
  one-time URL (`cer.mint`, ceremony.go:1359-1367; held ten minutes, preview.go:157-159).
- The prompt mailbox: one prompt at a time, answered once by its id and kind through
  `SubmitSecret` (ceremony.go:200-224), which the shell feeds from the raw message channel
  (main_windows.go:316-335) and the page writes through `submitSecret`
  (frontend/src/lib/api.ts:42-47).
- Page: the remove dialog (frontend/src/components/KeysPage.svelte:599-611), the Re-key dialog
  and its backup tick (194-235, 617-630), the password's three dialogs
  (frontend/src/components/EntangledDialog.svelte:24-50); the recovery key's dialog
  (frontend/src/components/RecoveryReveal.svelte), mounted above every route at `StepRecovery`
  with no prompt (frontend/src/App.svelte:55-64, 191), its three ways out and no second
  confirmation (frontend/src/lib/reveal.ts:1-12; ruled 2026-09-09, APP.md:2582-2584,
  DECISIONS.md:3329-3334).

Missing (docs/SCOPE.md:45; docs/DESIGN.md:384-421; DECISIONS.md:5245-5257):

1. The factual question at a removal, and its default (DESIGN.md:399-404): the remove dialog
   asks nothing (KeysPage.svelte:603-608).
2. Rotation at a password change, defaulted (DESIGN.md:406-409), and at turning the password on,
   which DESIGN.md:384 counts among "every ... credential change": neither rotates
   (mutate.go:310-364).
3. The change and its rotation in ONE ceremony and one commit: no app act makes two keystore
   writes (keys.go:131, 858, 877), and no keystore call rotates a slot set other than the live one
   (mutate.go:665, 714).
4. The plain sentence that revocation protects the future, not the past (DESIGN.md:418-421). The
   remove body says only the past half, and wrongly for a YubiKey or a password: "Copies and
   backups of the vault made earlier still open with it" (strings.ts:1545), while a backup holds
   only the active recovery slots (mutate.go:814-841, `Export`), so a removed YubiKey or password
   opens no backup.
5. A pre-check: `Core.RemoveSlot` has none, so an invariant refusal arrives after the user has
   proved an unlock method (keys.go:849-864; compare SetEntangled's, keys.go:66-81).
6. **The vault file keeps the state before a change, and nothing retires it reliably.** The
   losing superblock copy still names the previous slot region and registry, both intact;
   TestCrashBeforeFlip restores one superblock sector after a committed rotation and the previous
   state unlocks (internal/keystore/keystore_test.go:1136-1175). Whoever holds the file can read
   either copy. After a removal that state holds the removed slot; after a rotation it is the one
   place the retiring VMK still opens a registry, and through it every archive key and every
   escrowed recovery key of the moment (FORMAT.md:723-737, R38). **A registry-only save does not
   end it**: `updateRegistryAt` commits a `txn` with no slots and no header (unlock.go:386-399),
   so `commit` writes no region (file.go:345) and the abandoned region copy keeps its bytes —
   the removed record among them — even once both superblocks have moved on. Revision 1's "the
   next commit of any kind retires it" was false (outside review, finding 2).
7. **The "lost" path cannot be proved when the YubiKey removed is the vault's only one**: the
   invariant allows removing it while two recovery keys remain (internal/keystore/slots.go:155-197),
   and the proof then waits for "a YubiKey that already unlocks this vault" (keys.go:285), the
   lost one, until it times out.
8. **The minimal vault whose only YubiKey is lost cannot enrol its replacement.** One YubiKey and
   one recovery key: *Remove…* is greyed by the invariant (the recovery key alone would remain,
   slots.go:189-196), and *Add an unlock method…* proves with the lost YubiKey, since
   `acquireUnlocked` takes the token branch whenever one is enrolled (keys.go:255-266, 285). This
   is the case the owner ruled the feature for (DECISIONS.md:5253).
9. **No act replaces the recovery keys.** R38 says a VMK exposure is a recovery-key exposure that
   no rotation revokes (FORMAT.md:733-737), and the cure is "replacing every recovery slot ...
   and then rotating"; a one-step act was left undone on 2026-09-07 (DECISIONS.md:2573-2575).
   Done by hand (add, then remove), it escrows the new key under the current VMK while the exposed
   one still opens it (`AddSlot` seals the escrow under the live `KWK_secrets`, mutate.go:215-221).
10. **Old registry ciphertext stays in the vault file.** `commit` writes exactly the sealed
    registry (file.go:389-404) where `registryTarget` puts it (447-452) and trims only past the
    later of the two named registries (425-433), so a shorter registry written over a longer one
    leaves the longer one's tail in the gap, and the bytes past a registry's tag in its last 4 KiB
    unit keep whatever they held. The registry is AES-GCM, a counter mode, so whoever holds the
    Metadata key such ciphertext was sealed under and knows one block of its plaintext reads the
    rest (2.2.3; the outside review's second reading, finding 1).

Found on the way, fixed by this design (read, not run): the Re-key dialog's *Back up, then
re-key* never re-keys. `rekey(p)` (KeysPage.svelte:227-233) awaits `exportTo`, whose
`Keys.ExportBackup` resolves as soon as the export's ceremony has BEGUN (keys.go:907-921 into
190-233: `go cer.run`, then `return nil` at 232); it then calls `RotateNow`, which `beginWithVMK`
refuses with `ceremony.in_progress` while `c.cer` is set (keys.go:201-204; cleared only in
`finish`, internal/app/ceremony.go:293-298). The page shows "An unlock is already in progress."
(strings.ts:250); the backup is saved and the vault is not re-keyed, against APP.md:3900-3901 and
DECISIONS.md:3055-3056. The mock hides it: `begin_act` tests `CEREMONY["kind"]`
(tools/uimock/server.py:2659), which only the first-way-in flows set. The outside review confirmed
it (its Q1 position).

Found on the way and left as follow-ups (section 6), each read here:

- The specs disagreed on the recovery key's second confirmation: SCOPE.md:41-43 and
  DESIGN.md:348-351 still say "written down with a second confirmation" (2026-09-07), while
  APP.md:1335-1337 and 2582-2584 and the code (reveal.ts:4-8) say none, ruled 2026-09-09
  (DECISIONS.md:3329-3334). Ruling 2 of 2026-10-27 quoted the 2026-09-07 words; the owner has since
  ruled (R-B) that 2026-09-09 holds everywhere, the replacement's new keys included, and section 4
  brings SCOPE and DESIGN to its words (4.2, 4.3). No longer a follow-up.
- A vault left with recovery keys alone is "setup needed" (`setupNeededLocked`,
  internal/app/vaultfile.go:200-210). Its lock screen draws *Finish setting up…* as the accent
  and *Unlock with recovery key* beside it (frontend/src/components/Lock.svelte:735-737).
  *Finish setting up* runs its whole ceremony (the recovery key, a first unlock method chosen,
  its PIN and touch, the vault's password chosen afresh) and only then `AddFirstWayIn`
  (vaultfile.go:614) refuses a header whose password is on (mutate.go:245-247) with `ErrParams`,
  which the page words "Enfold refused the request." A header whose password was turned off is
  zero again (`newHeader(nil)`, mutate.go:41-44), so only a vault whose password is on now meets
  the refusal. Before this design the lost path could not reach that state (the proof waited for
  the lost YubiKey, keys.go:285); ruling 3 makes it one removal away. This design refuses *Finish
  setting up* before any ceremony while the password is on, greys it on the lock screen with its
  reason and gives the accent to *Unlock with recovery key* (2.3, 3.8); a lock-screen path that
  enrols through `AddSlot` stays a follow-up (section 6).

---

## 2. The core change

### 2.1 (a) The API: five bound methods take the choice; one bound method is added

| Bound method (internal/app/api/api.go:350-377) | Today | After | Call ID |
| --- | --- | --- | --- |
| `Keys.RemoveSlot` | `(recipientID string) error` | `(recipientID string, rekey bool, backupPath string) error` | 1280438677 |
| `Keys.ChangeEntangledPassword` | `() error` | `(rekey bool, backupPath string) error` | 3747209322 |
| `Keys.SetEntangled` | `(on bool) error` | `(on, rekey bool, backupPath string) error` | 2247223905 |
| `Keys.RotateNow` | `() error` | `(backupPath string) error` | 332274822 |
| `Keys.BeginEnroll` | `(kind, label string) error` | `(kind, label string, withRecoveryKey bool) error` | 309727738 |
| `Keys.ReplaceRecoveryKeys` | — | `(backupPath string) error` (new) | 3877776464 |

Why parameters, not new methods, for the first five: each is still ONE act, with one dialog, one
ceremony and one *Start again* (APP.md:3829-3846); the choice changes what its commit writes, or
how it proves, not what the act is. The Wails call ID is FNV-1a/32 over the package path and
`Struct.Method` alone (tools/uimock/server.py:2751-2754), so a parameter keeps the ID: the five
IDs above are the ones the mock already keys (server.py:2748-2757, 2787-2792), computed
again for this revision (FNV-1a/32 of
`github.com/dreamxwarden01/enfold/internal/app/api.Keys.<Method>`), and the new method's,
3877776464, is not among the mock's keys. Why a new method for the replacement: it is an act of
its own, with its own dialog and command (ruling 2). `backupPath` is "" for no backup; a path is
not a secret, so it may ride a bound argument (the rule is for typed secrets,
EntangledDialog.svelte:5-7). `withRecoveryKey` is the user's statement at *Add an unlock
method…* that no YubiKey of the vault is at hand (2.3, 3.4); it is refused unless the method added
is a YubiKey or a recovery key and the vault has a YubiKey and a recovery key.

What the binding needs: the six Go methods, api.go's wrappers, six lines of `TestBoundSurface`
(internal/app/api/api_test.go:78-87, plus one for the new method) and the regenerated, committed
frontend/bindings (`wails3 generate bindings -clean=true -ts -i`, build/Taskfile.yml:50-62, "a
diff is a review item"): keys.ts changes five signatures and gains one, and models.ts gains the
`CeremonyState` fields and steps of 2.5 with the `AfterFacts` type, `BackupStatus.vaultRekeyed`
and `.keysReplaced`, and `FileInfo.rekeyed` (2.3).

### 2.2 (b) One commit for the change and its rotation; the retiring commit, made durable

#### 2.2.1 One commit

Decision (kept from revision 1; the outside review's Q4 agrees): the change and its rotation are
ONE commit, through one internal rotation that takes the slot set the commit writes.

| Shape | A crash, a failure or a lock between the steps leaves |
| --- | --- |
| Remove, then rotate | The slot gone and the VMK not rotated: the un-rotated removal DESIGN.md:391-395 calls weaker than it looks and silent when it fails. Nothing on disk records a rotation owed: `rotation_pending` is written zero and ignored (docs/FORMAT.md:933), against DECISIONS.md:560-561. The panel would say Failed while the removal stood, and *Start again* would answer `vault.slot_not_found` (strings.ts:239). |
| Rotate, then remove | Worse: the rotation wraps the new VMK into the slot about to go (mutate.go:714-723), so a stop between them leaves the lost credential opening the NEW VMK. |
| Change, then rotate | A new password over an unrotated VMK, which DESIGN.md:406-409 says "accomplishes nothing". |
| Add new recovery keys, remove the old, then rotate | The new keys escrowed under the VMK the exposed ones still open (mutate.go:215-221) until the last step lands: whoever holds an exposed key and the file between the steps reads the new ones (outside review, Q2). |
| One commit (chosen) | The old state whole or the new state whole. FORMAT §8 already words it: step 3 keeps the escrow records "active in the region this commit writes ... a removal-then-rotation drops the removed slot's record" (FORMAT.md:1284-1286), and the whole lands in one flip (1301-1304). No layout change. |

The house precedent is the same: where two changes had to land together the keystore grew one
call (`AddFirstWayIn`, mutate.go:229-294; `writeEntangle`, 366-408).

The keystore change (internal/keystore/mutate.go, and `commit` in file.go):

```go
// recoveryAdd is a new recovery slot's escrow plaintext, sealed by rotate
// under the new KWK_secrets in the commit that creates the slot (R38).
type recoveryAdd struct {
	rid [16]byte
	key kdf.RecoveryKey
}

// rotate is §8 over the records and the header this commit writes. slots are
// the records that survive or arrive (a clone of the live set for a plain
// rotation; without the removed record for a removal; without every recovery
// record and with the prepared ones for a replacement), already checked
// against the invariant by the caller. hdr nil copies the live header
// forward; non-nil, it is the header a password change or turning the
// password on writes, and kpNew is the K_P that header derived: the
// entangled_key record is written from it (replacing the record there, which
// must still open; created where there was none) and every hardware slot is
// wrapped from it. hdr and kpNew are both nil or both set. adds are the escrow
// plaintexts of the recovery records in slots that the live set does not
// have; every other recovery record in slots must already have its escrow.
// One commit; on success the handle holds the new VMK and generation, and
// kpNew is still the caller's to install (installKP).
func (u *Unlocked) rotate(slots []format.SlotRecord, hdr *format.SlotRegionHeader, kpNew *[32]byte, adds []recoveryAdd) error

// Rotate performs the VMK rotation of §8 over the live slot set. The caller
// retires the losing copy with Retire (R41).
func (u *Unlocked) Rotate() error // mutable(), then u.rotate(cloneSlots(u.k.slots), nil, nil, nil)

// RemoveSlotAndRotate removes the slot with this recipient ID and rotates the
// VMK in the same commit (§8): the record leaves the set that step 3 prunes
// against and step 4 wraps, so its recovery_escrow record is dropped, never
// re-encrypted, and it is never wrapped the new VMK. ErrNotFound and the
// invariant's errors refuse before any key is drawn.
func (u *Unlocked) RemoveSlotAndRotate(recipientID [16]byte) error

// ChangeEntangledPasswordAndRotate is ChangeEntangledPassword and Rotate in one
// commit: a fresh entangle_salt and K_P, a new VMK and generation, every active
// slot wrapped the new VMK (hardware ones from the new K_P), and the
// entangled_key record holding the new K_P under the new KWK_secrets; the old
// K_P is in no record of the new state (DESIGN trap 30). ErrParams while the
// switch is off.
func (u *Unlocked) ChangeEntangledPasswordAndRotate(e Entangle) error

// SetEntangledAndRotate is SetEntangled(true, &e) and Rotate in one commit:
// the header's switch on with a fresh entangle_salt, a new VMK and generation,
// every hardware slot wrapped from the new K_P, and the entangled_key record
// created under the new KWK_secrets. ErrParams while the switch is already on;
// ErrInvariant, from the sets the new header would make, before the password
// is derived (as SetEntangled orders it, mutate.go:320-327).
func (u *Unlocked) SetEntangledAndRotate(e Entangle) error

// PreparedRecovery is a recovery slot made ready for ReplaceRecoveryAndRotate:
// its recipient ID and slot salt drawn and its public keys derived from the
// key (§3.1, §6.3), nothing wrapped yet. It holds the key itself, in a locked
// page as the Unlocked holds the VMK and K_P (unlock.go:44-61), until Zero.
type PreparedRecovery struct {
	rec format.SlotRecord
	key *secmem.Buffer // the 16 bytes of R; Free on Zero
}

// RecipientID is the prepared slot's recipient ID, whose first four bytes are
// the key's ID (§18.4); fixed before the key is shown, so the sheet names the
// slot that will exist.
func (p *PreparedRecovery) RecipientID() [16]byte

// Zero erases the key the prepared slot holds and gives its page back.
func (p *PreparedRecovery) Zero()

// PrepareRecovery makes a recovery slot ready for a replacement: label is the
// label it will carry, and others are the slots this replacement has already
// prepared. A recipient ID whose first four bytes are those of an active slot
// or of one of others is drawn again, so that a new sheet's ID never equals
// one the vault lists or another new sheet's (§18.4). The caller's r is
// copied into the prepared slot's page; the caller zeroes its own. Reads
// only; current(), not mutable().
func (u *Unlocked) PrepareRecovery(r kdf.RecoveryKey, label string, others []*PreparedRecovery) (*PreparedRecovery, error)

// ReplaceRecoveryAndRotate replaces every recovery slot with the prepared ones
// and rotates the VMK, in one commit (§8, R38): every record of slot_type 3 is
// removed whatever its state, the prepared records join the set, and the
// rotation prunes the old keys' recovery_escrow records, seals the new keys'
// under the new KWK_secrets, and wraps the new VMK into every active slot, the
// new ones included. ErrParams with none prepared; ErrDuplicate for a
// recipient ID already in the set or prepared twice; the invariant's errors,
// before any key is drawn.
func (u *Unlocked) ReplaceRecoveryAndRotate(news []*PreparedRecovery) error

// Retire finishes the retirement the file owes (R41), in full: while
// Keystore.RetireOwed holds, a retiring commit where one is needed — the live
// slot region, byte for byte and the rest of the copy zeroed, written into the
// copy the live superblock does not name, never into the live copy, and the
// registry re-sealed with only its date advanced, at the handle's generation,
// the superblock going to the losing copy as every commit's does, its scrub
// following its flip — and the scrub alone where only unnamed registry units
// remain. One retiring commit retires a file this writer made; a file that
// carries a legacy tail (bytes past slot_region_len in its live copy, or
// past the tag in its live registry's last 4 KiB unit) takes a second, once
// that copy and that registry are no longer live. Never a third: a file
// still owed after two is a corruption (corrupt, wrapping format.ErrInvalid).
// A scrub's I/O error is returned and leaves the file owed and the handle
// sound. Nothing is written when nothing is owed. On a tampered region (R25)
// nothing is copied forward: a registry-only commit stands in for each
// retiring commit, zeros go over the slot region copy neither superblock
// copy names, and the scrub runs; ErrTampered is returned only while the
// live copy's own tail remains, which only a region write could clear
// (2.2.3).
func (u *Unlocked) Retire() error

// Retire is the same, made through a Session, which holds the Metadata key
// and the generation: the app's barrier uses it to finish an owed retirement
// before a mutation that writes a record or a secret, or a backup (2.4).
func (s *Session) Retire() error

// RetireOwed reports whether the file still holds a state, or a piece of
// one, that a reader of the live state does not need (R41): (1) the live slot
// region copy holds anything past slot_region_len but zeros; (2) the other
// copy holds anything but the live region followed by zeros, or zeros alone;
// (3) the losing superblock copy fails its checksum, carries a vmk_generation
// other than the live one's, or names a slot region copy whose bytes
// [0, its slot_region_len) are not exactly the live region; (4) a 4 KiB unit
// of the registry area, [0x42000, the file's physical end k.end), holds a
// nonzero byte outside both registry extents the superblock copies name.
// Read from the file's bytes at Open; set before a commit writes its first
// byte, so a commit that fails leaves it set; computed again after every
// flip (2.2.3, rules 7 and 8). A read of memory, no I/O.
func (k *Keystore) RetireOwed() bool

// RetireRemnantOnly reports whether what is owed is the fourth clause
// alone, through whole unnamed registry units: no slot region copy and no
// superblock copy holds an earlier state, only earlier registry ciphertext
// does, which a scrub clears with no commit. The act's end words its bar
// by it (3.6). A read of memory, no I/O.
func (k *Keystore) RetireRemnantOnly() bool

// without is RemoveSlot's first half, shared: clone the live records, find
// this one (ErrNotFound), delete it, and check the invariant over the rest
// under the live header.
func (u *Unlocked) without(recipientID [16]byte) ([]format.SlotRecord, error)
```

`rotate` is today's `Rotate` body (mutate.go:600-737) with these edits and nothing else moved:

1. `mutable()` moves out to the exported callers, as each one's first line; `hdr` and `kpNew`
   not both nil or both set is `ErrParams`, before any key is drawn.
2. Line 665 becomes `pruneSecrets(reg, slots)`: the set this commit writes, not `u.k.slots`.
3. The secrets loop (670-691) still opens EVERY kept record under the retiring KWK_secrets, the
   entangled_key included, and a record that does not open fails the rotation (fail closed, as
   now; FORMAT.md:1288-1290). With `kpNew` set, the entangled_key's plaintext is zeroed and
   discarded at once and the record is sealed from `*kpNew` under KWK_secrets'; after the loop,
   with `kpNew` set and no entangled_key record met (turning the password on), one is created from
   `*kpNew` (`reg.SetSecret`, which keeps the section's order). The K_P step 4 wraps from is
   `kpNew` when set, else the record's plaintext. The defer at 670-674 zeroes whatever `kp`
   points at today; it is narrowed to the record's own plaintext buffer, so that it never reaches
   `*kpNew`, which the caller installs after the call (`installKP`, as 357-363).
4. After the loop and the vmk_history record (695-703), each `adds` entry is sealed with
   `sealSecretWith(kwkSNew, vaultID, format.SecretRecoveryEscrow, a.rid, a.key.Padded())` and set;
   an `adds` entry whose `rid` is not an active recovery record of `slots`, or a recovery record of
   `slots` left without an escrow record, is `ErrParams` before step 4 (R38: "every recovery slot
   has its record from the commit that creates it", FORMAT.md:740-741).
5. Line 706 becomes `requireEntangleKey(h, wrapKP)`, `h` being `*hdr` when set, else `u.k.hdr`.
6. Lines 714-723 iterate the `slots` argument (no second clone): a prepared record has the
   `SlotPubkey`, `MLKEMEK` and `RecipientID` that `wrapHybrid` reads (slots.go:309-324), so step 4
   wraps it at `newGen` like any other, filling its `EPK`, `MLKEMCT`, `WrapNonce` and
   `WrappedVMK`; no new record is ever wrapped at the retiring generation. Line 730 commits
   `txn{slots: slots, hdr: hdr, reg: reg, meta: metaNew, gen: newGen}`; with `hdr` set, the
   agreement check of `commit` (file.go:327-333) runs against the new header.

`RemoveSlot` becomes `without`, then `commitSlots(u.k.hdr, slots, nil)`, unchanged in behaviour.
`RemoveSlotAndRotate` is `mutable`, `without`, `rotate(slots, nil, nil, nil)`.
`ChangeEntangledPasswordAndRotate` is `mutable`; the switch-off refusal (as 351-353); `newHeader(&e,
u.k.sb.VaultID)`; `rotate(cloneSlots(u.k.slots), &hdr, kp, nil)`; `kdf.Zero(kp[:])` on failure and
`installKP(kp)` on success (as 357-363). `SetEntangledAndRotate` is `mutable`; `ErrParams` when
`u.k.hdr.Entangle`; `checkInvariant(format.SlotRegionHeader{Entangle: true}, u.k.slots)` BEFORE
`newHeader`, so the verdict comes before Argon2id runs; then as the change. `PrepareRecovery` is
`current()`; `newRecord`'s recovery case (slots.go:245-254) without its last line's `wrapHybrid`,
split out as `recoveryRecord(r, label, vaultID)` so that `newRecord` calls it and then wraps; the
redraw of the recipient ID on a 4-byte collision with an active slot or with `others`; the key
copied into `secmem.New(kdf.RecoveryKeySize)`. Why a page: the prepared keys stay in memory through
the keep and confirm steps, which may last until the prompt's deadline, beside a VMK and a K_P that
live in locked pages, and a recovery key is worth as much as either. `Session.Retire` is the same
loop over the Session's own Metadata key and generation; its tampered verdict, which decides whether
`Retire` takes its Tampered branch (2.2.3), is `k.reg.VerifySlotRegion(k.region)`
(format/registry.go:454-459), the check an `Unlocked` made at unlock and keeps as `u.tampered`.
`ReplaceRecoveryAndRotate` is `mutable`; `ErrParams` for an empty `news`; the clone of the live set
without any `format.SlotRecovery` record; each prepared record appended (`ErrDuplicate` as above);
`checkInvariant(u.k.hdr, slots)`; then `rotate(slots, nil, nil, adds)`, the `adds` copied out of the
prepared slots' pages for the call and zeroed by a defer when it returns. Replacing N recovery keys
with N new ones keeps the invariant by construction — the other records are untouched and a recovery
slot's secret set is its own key, disjoint from every other (slots.go:155-197) — so a vault whose
only methods are recovery keys, which has at least two, gets at least two fresh ones; the check
stays as the backstop.

#### 2.2.2 Why a retiring commit at all

After any commit the losing superblock copy names the state before it (section 1, item 6). After
a removal and re-key that is a state in which the removed YubiKey (with its PIN, and the password
while it is on) opens the retiring VMK, and that VMK opens the state's registry: every archive key
and every escrowed recovery key of the moment, and the recovery keys open the current vault.
After a password change, the same with a YubiKey and the OLD password; after turning the password
on, with a YubiKey ALONE; after a replacement, with any OLD recovery key; after a plain removal,
the removed method opens the CURRENT VMK. The 2026-09-07 ruling declined keeping the old file for
exactly this reason, "a retained copy re-opens the removed slot" (DECISIONS.md:2950-2951); the
losing copy and the abandoned region copy are such a copy, inside the file. So every act that
writes the slot region is followed, in its ceremony, by a commit that retires it (a new FORMAT
rule, R41, 4.4), re-keying or not: a plain removal's body says the method "stops unlocking this
vault now", which the abandoned copy would otherwise make untrue.

Nor is a registry gone because no superblock copy names it any longer: its ciphertext stays where
it was written until a later write lands on it, readable to whoever holds the Metadata key it was
sealed under (2.2.3, clause 4; section 1, item 10). After a rotation those are the registries the
retiring VMK opens, every archive key and escrowed recovery key of their moments in them, so the
retirement clears them too, and every commit clears what its own flip leaves unnamed.

#### 2.2.3 The retirement, durable (ruling 4; R-C, finding 1)

**The mark is the file's own bytes.** A retirement is owed exactly while the file still holds a
state, or a piece of one, that a reader of the live state does not need. Four places can hold
one (FORMAT.md:839-842 gives the layout):

1. the live slot region copy past `slot_region_len`: anything but zeros (the tail of a longer
   earlier region, which a writer before this change leaves, since `commit` writes only the
   encoded length today, file.go:355-356);
2. the other slot region copy: anything but the live region followed by zeros, or zeros alone;
3. the losing superblock copy: one that fails its checksum, carries a `vmk_generation` other than
   the live one's, or names a slot region copy whose bytes `[0, its slot_region_len)` are not
   exactly the live region;
4. **the registry area outside the two registries the superblock copies name** (new in revision 4;
   the outside review's second reading, finding 1): any 4 KiB unit of `[0x42000, k.end)`, up to the
   file's physical end (rule 8), that holds a nonzero byte lying outside both the live registry's
   extent and the losing superblock copy's (each `registry_off` through `registry_off + registry_len
   + 16`; the losing copy's only when it passes its checksum and its extents lie inside the file,
   since one that does not is owed by clause 3 already). The unit is FORMAT's atomic-write and
   alignment unit (FORMAT.md:1728-1732), and it is the one the scrub below writes.

**Why clause 4: leftover registry ciphertext is not inert.** Revision 3 said that a registry
extent no superblock copy names keeps its ciphertext harmlessly, "its nonce having lived only in
the superblock copy since overwritten". That is wrong, and it is struck everywhere (here, R41 and
7.5). The registry is AES-256-GCM (`registryAEAD`, file.go:226-231), a counter mode: each 16-byte
block of ciphertext is its plaintext XOR the AES encryption, under the Metadata key, of a counter
block that holds the nonce and the block's number. Whoever holds the Metadata key a registry was
sealed under and knows the plaintext of one of its blocks recovers that counter block by one AES
decryption of the ciphertext XOR the plaintext, reads the nonce out of it, and decrypts every
other surviving block of that registry; authentication is needed to trust what is read, not to
read it. Known plaintext is at hand: every registry of a vault begins with the same 16 bytes, its
format version and the first twelve bytes of `device_id` (`Encode`, format/registry.go:387-393),
and an earlier copy of the file gives its holder whole records at known offsets. And `commit`
leaves such ciphertext behind as a matter of course: it writes exactly the sealed bytes
(file.go:389-404), `registryTarget` puts the new registry at `0x42000` when it fits below the
live one and otherwise after it (file.go:447-452), and the trim keeps everything up to the end of
the later of the two named registries (file.go:425-433). A registry that shrinks at `0x42000`
leaves the tail of the longer one it overwrote in the gap before the next one; a 4 KiB unit's
bytes past a registry's tag keep whatever the unit held; a commit that fails after writing its
registry leaves that registry, whole or torn, in space no superblock names, and, when it was
written past the end the last flip computed, in space a handle that knew only that end would never
look at (rule 8; critique 3, finding 1). So whoever holds the
retiring VMK — through a removed YubiKey and an earlier copy of the file, say — reads from the
current file the archive key wrappers and escrowed recovery keys of every registry sealed under
it that still lies there, including archives created after that earlier copy was taken. This is
the outside review's cryptographic inference from the codec and the placement rule; it was not
reproduced here, and keystore tests 26 to 28 are built to reproduce it before they show it gone
(2.6).

The predicate is computed at `load` (file.go:103-142), which already reads both superblock
copies (111-119): it reads both region copies whole (256 KiB), the losing superblock's decode,
which `PickKeystoreSuperblock` (internal/format/keystore_superblock.go:172, through `pick`,
pick.go:66-83) is changed to return beside the winner's (today it returns only the loser's decode
error, `stale`), and the 4 KiB units of the registry area that are not wholly inside a named
extent — in a file this writer keeps, none or a few, since every commit scrubs them (below). The
steady state is not owed: after a retiring commit the losing superblock copy is the change's own
(the same generation, naming the other region copy, whose bytes are the live region); after a
registry-only save it is one save behind, naming the same region copy at the same generation; a
new file's copy B is copy A's at `seq` 0 (file.go:524-535), its region copy B is never written and
reads as zeros, and its one registry sits at `0x42000` with nothing after it (section 6, What
could not be confirmed).

Why the losing superblock as well as the regions (critique 2, F1): a retiring commit writes its
bodies, the barrier, then the superblock. If that superblock write fails, or a power loss takes it
after the barrier, the bodies are durable: the other region copy is already the live region, and a
predicate over the regions alone says nothing is owed. But the losing superblock copy still names
the state before the change, at its generation and with its registry's offset and nonce, and that
registry is under the retiring VMK's Metadata key. A losing copy that fails its checksum counts
too: a torn superblock write can leave the old offset and nonce in its unwritten part, which
Enfold's reader refuses and a hand-written tool does not.

Why the bytes and not a field:

- It is exactly the hazard. What a withdrawn credential can open in the file is a slot record in
  some region copy, and what a holder of the retiring VMK can read is a registry, named or not,
  sealed under its Metadata key. The predicate is true whenever any of them can remain, whatever
  the live superblock says, and false only when none can: every slot region byte is the live
  region or zero, the losing superblock names the live state at its generation, and every byte of
  the registry area is a named registry's or zero.
- It survives everything a mark must survive — a failed retiring write, a crash between the two
  commits, a scrub cut short, a reopen, a registry-only save, a copy of the file taken to another
  machine — without a write of its own, because it is what the failure left.
- It trusts nothing anyone can clear. A superblock mark would be checksummed and not
  authenticated, which is why `rotation_pending` is ignored on read (FORMAT.md:933): a writer that
  trusted a cleared mark would skip the retirement. A writer that trusts the bytes cannot be
  talked out of it, and one that finds junk retires — harmless, since a retiring commit changes
  no state and a scrub writes only bytes no superblock names.
- It needs no FORMAT field, and no reader rule changes: `slot_region_len` still says the rest of a
  copy is not part of the region and is not read (FORMAT.md:927), a reader still reads only the
  registry its superblock copy names, and still chooses that copy by its checksum and `seq` alone
  (FORMAT.md:851-852); only a writer reads the rest, to decide this. `rotation_pending` stays
  reserved.
- It retires files written before this change, including an imported copy, at their first unlock
  here.

Considered and declined: repurposing `rotation_pending` as a `retire_pending` mark
(unauthenticated, and blind to legacy files and to a region a registry-only save left behind
unless every writer also carried the bit); keeping the full second commit with such a mark (the
bytes already are the mark); and, for the registry, a key of its own per sealing, so that a
remnant needs more than the Metadata key (a layout and key-schedule change; zeros leave nothing to
reason about).

**The shape is the full second commit, written only into what is not live, and every commit
scrubs after its flip.** Ruling 4 leaves the mechanism to the designer. Two shapes meet clauses
1 to 3:

- *The full second commit* (chosen): `commit` itself, with the live region as its region and the
  registry re-sealed with only its date advanced. It reuses `commit` whole — its order, barrier,
  conflict check, trim, scrub, crash reasoning and fault seam — and it overwrites the losing
  superblock copy by the write every commit makes. Because it writes the copy the live superblock
  does not name and then flips to it, it also clears an old tail in the live copy without ever
  writing that copy in place: after the flip the old live copy is the other one, and a second
  retiring commit rewrites it whole (below). Its cost: a 128 KiB region write, a registry write (a
  few MB at most, FORMAT.md:847), the scrub, two or three fsyncs and a new `modified_at` per act,
  which no user waits on; and it needs the Metadata key, which is at hand wherever it runs: the
  act's `Unlocked`, the unlock's `Unlocked`, a Session (2.4).
- *The mirror and wipe* (the outside review's lighter shape): copy the live superblock into the
  losing copy at `seq` − 1, fsync, then zero the abandoned region copy. The reader already accepts
  that shape (a registry authenticates under its copy's `seq` or one more, file.go:237-262, and
  FORMAT.md:567-572 is the same shape for the archives' R31), it seals no registry and it moves no
  `modified_at`. Declined for two reasons. It is a second write path beside `commit`, with its own
  order, barrier and crash cases to prove. And it cannot clear an old tail in the live region copy
  without writing that copy in place, which is what FORMAT §4 doubles the region to avoid
  (FORMAT.md:886-888): the region's end is not sector-aligned, so a write of its tail rewrites the
  sector that holds its last bytes, and nothing falls back on a damaged live region (`load` fails
  on one that does not decode, file.go:126-133, and one that fails its hash is Tampered, which
  freezes every change to the unlock methods, FORMAT.md:477-480).

Clause 4 is met by neither shape alone. It is met by the scrub, which either shape would need and
which `commit` makes after every flip, so that the steady state stays clean and a retirement adds
nothing to it but its own commit.

Revision 2 gave the full commit a third reason, that both copies would then name one state
"through two bodies", so that a damaged live region or registry would still open through the
other. That was wrong and is struck (critique 2, F3): a reader chooses a superblock copy by its
checksum alone (FORMAT.md:851-852; pick.go:66-83), `load` fails on a region that does not decode
(file.go:126-133), and `Unlock` reports a registry that does not decrypt as corruption
(unlock.go:163-171), as FORMAT.md:781-783 says of the archives. No body-level fallback is added
either: it would be a rollback lever, a damaged live body opening the older state. The owner may
weigh the two shapes again on these terms; the predicate, the scrub, the tests and the app do not
change with the choice.

**What `commit` does (file.go:298-442):**

1. Every slot region write fills its 128 KiB copy: the encoded region is copied into a
   `format.SlotRegionSize` buffer whose rest is zero, and that buffer is written;
   `next.SlotRegionLen` stays the encoded length. `commit` writes only the encoded length today
   (file.go:355-356), so a shorter region leaves the tail of whatever the copy held, which after a
   removal can be the removed record itself.
2. **Every registry write fills the rest of its last 4 KiB unit with zeros**, in the same write as
   the sealed bytes and so before the barrier: from the tag's end to the next 4 KiB boundary, but
   never past `k.end`, the file's physical end before the commit (rule 8: bytes past it never held
   anything, so a registry written there is not padded and the file's length is its extent's end),
   and never into the live registry's extent, which a file another writer placed off the 4 KiB grid
   could begin inside that unit. The new registry never overlaps the live one (file.go:447-452) and
   the padding stops short of it by this rule, so nothing a reader of the live state needs is
   written; the padding may land on the losing registry, as the new registry itself may today. With
   every extent this writer places starting on the grid (`0x42000`, or the boundary after the live
   one) and its last unit ending in zeros, no 4 KiB unit is ever shared between a named registry and
   an unnamed remnant.
3. `txn` gains `retire bool`. A retiring commit writes `k.region` (verbatim, never re-encoded)
   into the copy `k.sb.LiveSlotRegion().Other()` names, filled to 128 KiB, then the registry and
   the barrier as now; `next.SlotRegionOff` is the copy it wrote and `next.SlotRegionLen` the same
   length. It never writes the live copy. The registry's `SlotRegionHash` is carried, not
   recomputed (as for a registry-only write, file.go:375-379): the bytes are the ones it already
   vouches for, and the commit is made only while the live region verifies against it.
4. **A registry-only commit while a retirement is owed carries one retiring pass.** A commit with
   neither `slots` nor `hdr` (every registry-only save, `updateRegistryAt`, unlock.go:386-399)
   made while `k.owed` holds by clause 1, 2 or 3 and while the live region still verifies against
   the registry it carries (`k.reg.VerifySlotRegion(k.region)`, format/registry.go:454-459) is made
   as a retiring commit (3) carrying its registry change. If that commit fails before its flip with
   anything but a conflict or an unknown outcome, nothing it wrote counts (file.go:293-297) and the
   save is made again at once as the plain registry-only commit it is: a registry write the app's
   barrier does not hold — a receipt, a name — never fails for the retirement's sake (R-A), and
   the file stays owed (rule 5). This makes progress on every save. It is not what holds a new
   record or secret back while a retirement is owed — the app's barrier is (2.4), since one pass
   does not finish a file with a legacy tail (the outside review's finding 3). A region that does
   not verify is never carried (R25): the save is then the plain commit it is, whose superblock
   write overwrites the losing copy as every commit's does, and `Retire`'s Tampered branch (below)
   does the rest of what needs no region copied forward.
5. **`k.owed` is set true before the commit writes its first byte**, so a commit that fails after
   writing anything — a region copy (critique 2, F5: a failed `AddSlot` of a YubiKey leaves its
   record, wrapping the current VMK, in the copy no superblock names) or a registry, whole or torn,
   in space no superblock names — leaves the handle owed exactly as its bytes are.
6. **The scrub, after the flip and the trim** (new; finding 1). `scrub` first reads the two
   superblock copies again (`checkOnDisk`, file.go:169-188) and writes nothing when another writer
   has committed since the flip: the handle is then Broken (`ErrConflict`), as for any conflict, the
   flip that preceded it having landed (critique 3, nit 12: the exclusive lock stops another Enfold,
   not a writer that ignores it, file.go:164-168). Then it reads every 4 KiB unit of `[0x42000,
   k.end)` that is not wholly inside the live registry's extent or the losing copy's, and for each
   one that holds no byte of either extent and holds a nonzero byte, writes zeros over it (the last
   unit only up to `k.end`); then it fsyncs whenever the handle owes one (`unsynced`, set before
   the trim's cut and before each of the scrub's writes, cleared only by that fsync; the outside
   review of the diff, findings 1 and 2, `TestARetirementLandsOnlyOnceFlushed`): zeros and a cut
   read back through the cache before the disk holds them, so neither the reread after a retiring
   commit nor a retry that finds nothing left to write is proof of them, a retry makes the fsync
   all the same, and until it succeeds `RetireOwed` holds. It never writes a unit that holds a
   byte of either named extent — never a live sector: the 4 KiB unit is the atomic-write unit
   FORMAT's superblocks already rest on (FORMAT.md:1728-1732), so a scrub's write never rewrites a
   sector a reader of either state reads — never anything below `0x42000` (the superblocks and the
   slot regions are written by commits, and by `Retire`'s Tampered branch alone outside one, below),
   never past `k.end`, and never a superblock, so it changes no state and has no commit point. A
   unit that holds both a byte of a named extent and a nonzero byte outside it — only the last unit
   of a legacy extent, written before rule 2, or an extent another writer placed off the grid — is
   not written: it keeps the file owed (`mixed`) until a later retiring commit has left that extent
   unnamed, after which the unit is the scrub's. The commit has landed whatever the scrub does: an
   error from it leaves `k.owed` true and is returned by `Retire` and logged by any other commit,
   never `ErrIndeterminate`, and the handle is not Broken — save for the conflict its own check
   finds, which marks the handle as `checkOnDisk` does while the commit still returns nil, its flip
   having landed, so that the next call on the handle meets it.
7. **After the flip, `k.owed` is computed again**: after a retiring commit, from the file (the two
   region copies, the losing superblock and the registry area's unnamed units up to `k.end`: 260 KiB
   and the units), so that an old tail the flip has just left in the copy that is no longer live
   keeps it owed; after a registry-only commit, clauses 1 and 2 keep their values (no region was
   written), clause 3 is false (the losing copy is now the previous live one, at the same
   generation, naming the live region copy), and clause 4 is what the scrub left; after any other
   region-writing commit (a change), owed by clauses 2 and 3, its retirement following at once. The
   clauses are kept apart in the handle (`retireState{liveTail, other, loser, remnant, mixed
   bool}`), so that `Retire` knows whether a commit is needed or a scrub suffices; `RetireOwed` is
   their union with rule 6's `unsynced`, which no reading of the bytes computes, and reads memory
   only.
8. **The handle knows the file's physical end** (`k.end`; critique 3, finding 1). `k.size` is the
   logical end the last flip computed (file.go:428-435), and `load` is the only stat
   (file.go:104-108). A commit that writes its registry past `k.size` and then fails before its
   flip — `registryTarget` puts a registry there whenever it grows or the live one sits at
   `0x42000` (file.go:447-452) — extends the file while `k.size` stays. The next commit in the
   same process lands at the same target or at `0x42000`, and the trim cuts only when its end is
   below `k.size` (file.go:429), so the failed registry's tail past the new one would survive
   every padding, scrub and recomputation bounded by `k.size`, `RetireOwed()` answering false over
   old-key ciphertext until the next `Open` stats the file. So the handle keeps `end uint64`, the
   file's physical end as far as this handle has written it: set from the stat at `load` (and so
   at `create`, which calls it), and raised to `off + len` before every registry write, padding
   included, whether the write then succeeds or not (a region write lies below `0x42000` and never
   raises it). After every flip the trim truncates to the later named extent's end whenever that
   end is below `k.end`, and sets `k.end` to it when the truncation succeeds, the scrub's fsync
   making the cut durable (rule 6); `k.size`, and with it
   `FileSize()`, is set to `k.end` after every flip, so the size the Vault page shows is the
   file's. Rule 2's padding, the scrub, `readRetire` and rule 7 all use `k.end`: the bytes a failed
   commit left past the last flip's end are cut by the next flip's trim, or, when the truncation
   fails, zeroed by its scrub, and counted by the fourth clause until then. A commit that fails
   before its flip does not truncate back what it extended (the critique's option): the next
   flip's trim and scrub clear it with no second path, which a failed truncation there would need
   a seam and a clause of its own to cover.

**`Retire`** is `current()`; then, while `k.owed`, at most two retiring commits and as many scrubs
as a failure leaves. When clause 1, 2 or 3 holds, or clause 4 by a unit that also holds a named byte
(`mixed`), a retiring commit — `commit(txn{reg: clone, meta: metadata key, gen: u.gen, retire:
true})`, whose own scrub follows its flip. When only whole unnamed units remain, or only the
scrub's fsync (rule 6), the scrub alone.
Nothing when nothing is owed; a scrub's error is returned (still owed); and a third retiring commit
would be a corruption (`corrupt`), which cannot happen: after two retiring commits both region
copies were written whole by them, the losing superblock is the first one's, both named registries
were written by this writer, on the grid and padded, and every unnamed unit was offered to two
scrubs. Only a file with an old tail in its live copy, or a legacy registry whose last unit holds
bytes past its tag, takes the second commit; every write from this change on fills its copy and its
unit. Each retiring commit stamps a new `modified_at`, as every commit must (R35,
FORMAT.md:688-691); the vault's "Last changed" therefore moves by the seconds between the change and
its retirement, and once or twice for a legacy file at its first unlock here. A scrub alone stamps
nothing.

**On a tampered region** (the live region fails R25: `u.tampered`, or
`k.reg.VerifySlotRegion(k.region)` for a Session) `Retire` copies nothing forward (critique 3,
finding 3, under R-C's "import or repair"), and does everything else:

- In place of each retiring commit it makes a registry-only commit with only the date advanced,
  which R25 allows and which carries the hash it was given (file.go:375-379), so the tampering is
  reported again at the next unlock. Its superblock write overwrites the losing copy, which clears
  clause 3; two of them leave a legacy registry's mixed unit unnamed, for the scrub.
- Once clause 3 is false, both superblock copies name the live region copy, so the other copy is
  named by neither. While clause 2 holds, `Retire` writes zeros over that whole 128 KiB copy
  (`zeroOther`, 2.6) and fsyncs. It is the one write below `0x42000` outside a commit, safe for
  the reason the scrub is: no superblock copy names the copy written, the two copies are 4 KiB
  aligned and 128 KiB long (FORMAT.md:839-842), so no unit is shared with the live one, and it
  changes no state; afterwards clause 2 is false ("zeros alone"), and the withdrawn record a
  removal left there is gone.
- Then the scrub, as everywhere.

What can remain is clause 1 alone, the live copy's own tail, which only a region write could
clear. While it holds `Retire` returns `ErrTampered`, having done the rest, and the import is the
cure (2.4). On a region this writer made, clause 1 never holds (rule 1), so a Tampered vault whose
change was made by this writer is retired in full without its region being written forward.
A registry-only save on such a region (rule 4) moves the losing copy and so may clear clause 3 on
its own, but it never touches the live copy: what the barrier decides about clause 1 cannot change
under a receipt.

**The retiring commit's registry placement.** The retiring commit places its registry by the
rule every commit uses (`registryTarget`, file.go:447-452) and needs no rule of its own; what
matters is where the registry sealed before the change, under the retiring VMK's Metadata key,
ends up. At the retiring commit the live registry is the change's (R_c) and the losing copy names
the one before it (R_b); the change placed R_c relative to R_b, then live, by that same rule:

| R_c at the change | R_r, the retiring commit's | What becomes of R_b |
| --- | --- | --- |
| At `0x42000`, below R_b | After R_c, at the 4 KiB boundary after its tag, which is at or before R_b's start (R_c fit below R_b, whose start is on the grid) | Overwritten from its start before the flip, wholly or in part; whatever lies past R_r's end is cut by the trim, which keeps only up to R_r's end since R_c lies below it (file.go:428-433), and is zeroed by the scrub when the trim fails (its failure is ignored, 430) |
| After R_b, and R_r fits below R_c | At `0x42000` | If R_b began at `0x42000`, its head is overwritten before the flip and its tail, when R_r is shorter, lies between R_r and R_c: unnamed after the flip, in whole units by rule 2, and zeroed by the scrub. If R_b began above `0x42000`, it lies between R_r and R_c whole and is zeroed |
| After R_b, and R_r does not fit below R_c (it grew) | After R_c | Untouched before the flip, unnamed after it, zeroed by the scrub |

In every case R_r never overlaps R_c, the live registry while it is written, and after the
retiring flip and its scrub the two named registries, R_r and R_c, are both sealed under the
current Metadata key, while no byte of R_b, nor of any registry before it, is left in the file:
overwritten, trimmed or zeroed. The one exception is rule 6's legacy unit, bytes past R_c's tag in
its last unit, which only a file whose change was written before this rule can hold and which the
second retiring commit settles. Before the retiring flip R_b is named by the losing copy and is
never touched by a scrub: that is the state the retirement is owed for.

Crash and failure semantics, the change and then its retirement:

| When | The file holds | What the user sees |
| --- | --- | --- |
| The change fails before its superblock write: a record that does not open (fail closed), an I/O error on the region or the registry, the fsync before the flip | The old state whole (FORMAT.md:1295-1304; file.go:293-297): the bodies went to the inactive region copy and to space the live superblock does not name. Owed, when anything was written (rule 5): the other region copy may now hold a record of the change, and the registry area a registry that never landed, which the act's own `Retire` clears at once (2.4, `landChange` step 3), or, when that fails too, the next gated mutation or the next unlock | The act ends Failed with its code (2.6) over "Nothing was changed." (3.6); the method is still listed; *Start again* repeats the act with the same choices. A replacement adds "The recovery keys shown were never added: destroy what you kept of them." (3.3) |
| Power loss before the change's superblock write | The old state whole, as above | The vault opens as before at the next start, and the unlock clears what the attempt wrote; the method is listed; the user does it again |
| The change's superblock write or its fsync fails | Old whole or new whole, unknown to this handle (`ErrIndeterminate`, file.go:413-423) | Broken (ceremony.go:262-268, internal/app/session.go:479-491): "A save could not be confirmed. Reopen the vault to see which version was kept." (strings.ts:249); Reopen shows the method listed (nothing happened) or gone and the vault re-keyed, never a removal without its re-key. A replacement's end says the new keys work only if the Vault page lists their IDs after the reopen, and to keep every sheet until then (3.3) |
| Between the change and its retirement: power loss, a crash | The new state live; the abandoned copy holds the old one, and the losing superblock names its registry; owed | The vault opens at the new state. The next unlock retires before the Session is installed (2.4) |
| The retiring commit fails before writing (the conflict check aside) | As the row above | The act ends Done — the change is true — with an attention bar under its title saying what the file still holds (3.6), the standing warning `vault.retire_owed` (2.5), no backup taken (2.3), and every mutation that writes a record or a secret held until the retirement lands (2.4) |
| The retiring commit fails after writing part of its bodies (the region, the registry, the fsync before its flip) | The new state live. The other region copy is overwritten wholly or in part with the live region. Enfold's reader no longer opens the losing superblock's state (its region does not decode, fails its hash, or holds records of another generation, a verdict of tampering, FORMAT §6.2); but a torn write can leave the withdrawn record whole past the part written, with the losing superblock and its registry intact, which a hand-written tool still unwraps (the record's AAD is its own bytes and `vault_id`, FORMAT.md:1023-1027). Owed by those bytes until a retiring commit lands | As the row above. (Revision 1 said such a failure "wrote nothing"; the bodies precede the flip, file.go:355-406, outside review finding 2) |
| The retiring commit's bodies are durable and a power loss takes its superblock write | The new state live; the other region copy is already the live region; the losing superblock copy still names the state before, with its registry's offset and nonce; owed by that copy (clause 3) | The vault opens at the new state; the next unlock retires, and that commit's superblock write overwrites the losing copy |
| The retiring commit's superblock write or fsync fails | Retired or not, unknown; both outcomes hold the new state | Broken, as for any `ErrIndeterminate`; Reopen shows the method gone and the vault re-keyed, and the next unlock retires if the bytes still owe it |
| After the retiring flip, its scrub is cut short or fails (a power loss, an I/O error) | The new state, named by both superblock copies; some unnamed units still hold old registry ciphertext; owed by clause 4 and, once the scrub has written, by rule 6's pending fsync (`unsynced`); when only that fsync failed, by the pending fsync alone, the zeros reading back through the cache. No superblock was written, so nothing about the state is unknown | When the act's own `Retire` returned the error, as the fifth row; after a power loss, the next unlock scrubs without a commit |
| After the retiring flip and its scrub, the scrub's fsync included | The new state, named by both superblock copies; nothing owed, save an old tail now in the copy that is no longer live or a legacy registry's last unit, which `Retire`'s second pass clears in the same call | The next Session is installed (2.4) or the facts cached, and the act ends Done; a lock landing here still ends Done |
| A retirement owed and the next registry save | The save's own flip carries one retiring pass (rule 4), and its own scrub follows | Nothing; the warning clears with it once nothing is owed |

Two Enfold processes cannot interleave with the act: the keystore is opened exclusively
(internal/keystore/lock_windows.go:40, `lockFile`), and every commit first compares the
superblock on disk with the handle's (`checkOnDisk`, file.go:164-188), so a write by any other
writer is caught before the commit and enters Broken (`ErrConflict`, ceremony.go:262-268); the
combined act never commits over a state it did not read. The scrub runs inside the call that made
the commit or inside `Retire`, under the same exclusive lock, so no other Enfold's registry can be
in the units it writes; a writer that ignores the lock (file.go:164-168) is caught by the scrub's
own `checkOnDisk` before its first write (rule 6), which narrows the window to the scrub's own
writes.

What retirement does not establish, said in R41: physical erasure (an SSD may keep the old bytes,
and so may the space a trim gave back, DESIGN.md:414-416), and anything about copies of the file
taken before it landed — including the dated copy an import keeps of a vault that still owes one
(`retire`, internal/app/vaultfile.go:258-295; APP.md:177-185), which the import's confirmation
therefore names (2.4; critique 3, finding 2).

The backup an act was asked for is a separate new file written AFTER the retiring commit, and
only once nothing is owed (2.3; R-C, finding 8), from the handle at the new generation, so nothing
it writes or fails to write touches the vault. A power loss during it leaves at most a torn file at
its path, which does not open; the vault is already re-keyed, no `lastExportAt` was recorded, and
the backups card says the last backup was made before the re-key (2.3), so the user exports again,
to another file name, since an export never overwrites (`O_EXCL`, file.go:500).

### 2.3 (c) The invariant, the pre-checks, the proof, and the backup after the act

What the keystore removal can refuse: `ErrNotFound` (`vault.slot_not_found`) and `ErrInvariant`
(`vault.invariant`); `ErrPolicy` and `ErrDuplicate` from `checkInvariant` (slots.go:155-197)
cannot arise from deleting a record out of a set that satisfied them; `ErrTampered`, `ErrStale`
and `ErrClosed` from `mutable()` (mutate.go:159-167).

The refusals reach the user before any ceremony, in two layers:

1. Unchanged: the page greys *Remove…* by `SlotView.Removable`, its reason as the title
   (KeysPage.svelte:104-108, 413), from `keystore.Removable` over the live header and records
   with no VMK (file.go:566-575), cached in the facts (session.go:180-190).
2. New: each act checks synchronously from the same cached facts, while Unlocked, before
   `beginAs` (2.5), the tampered reason first, as SetEntangled answers it (keys.go:70-74):
   - `RemoveSlot`: Tampered -> `vault.tampered`; the ID not among `c.vault.slots` ->
     `vault.slot_not_found`; `!c.vault.removable[rid]` -> `vault.invariant`.
   - `ChangeEntangledPassword`: its two checks, as now (keys.go:113-121).
   - `SetEntangled`: on, its two checks as now (keys.go:67-81); off with `rekey` or a backup ->
     `params` (turning the password off offers neither, 3.2).
   - `BeginEnroll` with `WithRecoveryKey`: kind password -> `params` (a password never joins a
     vault that has a YubiKey, slots.go:171-173, so it never meets the wait); no hardware slot
     among the facts -> `params` (a vault without a YubiKey never waits for one, so it never needs
     the switch); no active recovery slot -> `vault.no_recovery_slot`.
   - `ReplaceRecoveryKeys`: Tampered -> `vault.tampered`; `setupNeededLocked()`
     (vaultfile.go:200-210, every method a recovery key) -> `vault.setup_needed`; no active
     recovery slot -> `vault.no_recovery_slot`. The head's button is greyed in both states with
     reasons of its own (3.3), and no follow-up points to it there (3.5), so these refusals are a
     backstop the page does not reach.
   - `FinishSetup` (vaultfile.go:655-711), while the vault's password is on (`c.vault.entangled`,
     the header's switch, cached while Locked, session.go:183; APP.md:3725-3726) ->
     `vault.setup_password_on`, a new refusal, before any ceremony: `AddFirstWayIn` would refuse
     that header after the whole ceremony (mutate.go:245-247), and the vault's way back is an
     unlock with a recovery key, then *Add an unlock method…* on the Vault page, which proves with
     that recovery key and wraps the new YubiKey from the kept K_P (keys.go:268-283, 403-407).
   - Every act with a backup: a reserved name or a place in the data folder -> `params` (the test
     `ExportBackup` makes, keys.go:908-910, moved into `backupPlaceOK` and shared by all six);
     no active recovery slot left after the act (the one removed not counted) ->
     `vault.no_recovery_slot`, since a backup holds the recovery slots alone (mutate.go:825-827).
   - Every act but a reading one, last, inside `beginAs` and under the same hold of the state
     mutex as its ceremony's creation: the barrier (2.4), which first finishes an owed
     retirement in full and otherwise refuses with `vault.retire_owed` (or `vault.tampered` on a
     Tampered vault still owing a live copy's own tail after its repair, 2.4, or enters Broken on
     an unknown outcome).

   Locked, the checks are skipped and `beginAs` answers `vault.needs_unlock` (keys.go:192-196).
   The page's `begin` puts any refusal in its error InfoBar (KeysPage.svelte:64-66, 80-84); no
   touch is asked. The keystore's checks inside the ceremony stay the authority, as a backstop.

Why the replacement is refused on a vault whose methods are all recovery keys: no protector would
remain to show a new key again (the reveal is refused there, keys.go:714-716, by the rule that a
protector authorises a showing, DESIGN.md:344-346), and the next step on such a vault is adding an
unlock method anyway — the *setup needed* state the page already names (KeysPage.svelte:367-369).
Added first, the new YubiKey is then the proof, and every new recovery key can be shown again
from it. So the replacement never proves with the recovery key it replaces.

**The proof of a re-keying removal leaves out the YubiKey removed** (kept from revision 1,
narrowed). The ceremony carries the recipient ID it does not ask for (`cer.without`, and
`CeremonyState.Without` in hex, 2.5), set at begin for a re-keying removal of a YubiKey and of
nothing else. `acquireUnlocked` (keys.go:249-339) leaves it out when it counts `hasToken`
(255-266), which with `hasPassword` decides the branch, so:

- A vault with another YubiKey: the wait asks for one, and its line names only the others
  (CeremonyPanel.svelte:131 filters by `c.without`, 3.6). `tokenCredential` (288) is still handed
  every slot: the YubiKey being removed, inserted anyway, proves as it does today, since whoever
  inserts it is at the keyboard with its PIN, whereas handing the proof a list without it would
  park the ceremony on "no slot matches" (`StepNoMatch`, ceremony.go:846-855), a refusal that
  would read as if the YubiKey were not the vault's.
- A vault whose only YubiKey is the one removed, with a standalone password: impossible, since a
  password and a YubiKey never coexist (slots.go:171-173).
- **A vault whose only YubiKey is the one removed, and two recovery keys** (the invariant allows
  it, slots.go:189-196): the proof is a recovery key, by the branch that exists
  (keys.go:268-283), in the panel's recovery view (CeremonyPanel.svelte:308-314). This is ruling
  3's first case. The reveal's rule governs only the showing of an escrowed key.
- A removal of a password: `without` is not set, so the password branch (keys.go:323-339) runs as
  today and accepts whichever password slot matches, the one being removed included: nothing is
  listed or waited for there, and refusing the user's own typing would only be friction. On a
  vault of one standalone password and two recovery keys (the two recovery slots are disjoint,
  slots.go:189-196), the re-keying removal of that password is therefore proved by typing it, not
  by a recovery key: revision 2 set `without` for a password too, which sent that user to fetch a
  recovery sheet and made a third recovery-key case that ruling 3 did not make (critique 2, F8).

What the recovery-key proof adds is said with care (critique 2, F7). Cryptographically it adds
nothing: whoever holds a recovery key unlocks the vault from the lock screen and so holds the
VMK, with which any record can be written and R25's hash sealed again; the proof gives no power
the VMK it yields does not already give. Through the app it adds reach. Before ruling 3, on a
vault with a YubiKey, a recovery key could unlock and read but authorise no act, since
`acquireUnlocked` took the token branch whenever one was enrolled (keys.go:255-266, 285). Now a
stolen recovery key, with the vault file and Enfold, can enrol a YubiKey of the thief's own, and
that YubiKey then authorises a showing of the other recovery keys, a removal of the owner's
YubiKeys and a replacement of the owner's recovery keys; and it survives *Replace recovery
keys…*, which replaces recovery slots only. The same reach existed for a thief with a
hand-written tool; the switch makes it a few clicks. So the replacement's "when it is needed"
line and the recovery key's removal say to check the unlock methods and remove any the user did
not add (3.1, 3.3), and keystore test 21's whole cure includes that removal.

A recovery key's removal and every removal answered "kept" prove as today.

**The recovery-key proof at *Add an unlock method…*** (ruling 3's second case). The ceremony carries
`cer.recoveryProof`, set at begin from `EnrollOptions.WithRecoveryKey`; `acquireUnlocked` takes the
recovery branch (keys.go:268-283) when it is set, whatever the vault holds: `useRecovery :=
cer.recoveryProof || (!hasToken && !hasPassword)`. The rest of the enrolment does not change. For a
YubiKey, `releaseUnlocking` finds no card held and returns no public key (keys.go:434-443), so
`enrollToken` waits for no swap and asks at once for the new YubiKey (455-466), which proves itself
by its PIN and touch before `AddSlot` (533-600) and is wrapped from the kept K_P, so a vault with
its password on asks no password here (406-407). For a recovery key, the new key is drawn, committed
and shown after its commit, as every added one is (keys.go:379-401). How the user reaches it is the
page's (3.4): a subtle *Use recovery key instead* under the opening wait of an enrolment's proof,
and at its *No match* the accent *Use recovery key* with the step's own body, as the lock screen
draws both (Lock.svelte:793, 796-809); either cancels that ceremony and begins the act again with
`withRecoveryKey`, with the lock screen's wait for the cancelled ceremony to end
(Lock.svelte:419-444). The core needs no mid-ceremony change of proof. Why a switch on the wait
rather than a question in the Add dialog: the user who lost a YubiKey meets the wait for it at the
moment the switch is needed, and every other enrolment keeps its dialog as it is; and it is the lock
screen's pattern, one way of saying one thing.

Never otherwise (ruling 3): no other act takes `WithRecoveryKey`; the switch is drawn only for an
enrolment of a YubiKey or of a recovery key on a vault that has a YubiKey and a recovery key; and
a removal proves with a recovery key only where the YubiKey it leaves out was the vault's last
and no standalone password remains (a vault whose methods are all recovery keys already proves
with one, as today).

Why a recovery key's enrolment takes the switch too (critique 3, M6; revision 4 limited it to a
YubiKey and gave no reason). Ruling 3 names the user's word that the YubiKey is lost, not the kind
added. A user whose only YubiKey is lost and who has no other yet could otherwise not withdraw it:
its *Remove…* is greyed by the invariant ("add a new one or a recovery key first", 3.1), and
adding a recovery key would wait for the lost YubiKey, with *Cancel* alone. With the switch the
second recovery key is added on the first one's proof, *Remove…* on the lost YubiKey is then
enabled and proves with a recovery key (the first case above), and the vault, left with recovery
keys alone, is pointed to adding a YubiKey once there is one (3.6). Through the app this reaches
less than the YubiKey's switch already does: a recovery key added so is replaced with the others
by *Replace recovery keys…*, and it authorises no showing. Its showing says that it can be shown
again only once a YubiKey or a password is added (3.4), since none is at hand to prove with.

**The backup after the act** (ruling 1):

```go
// backUpAfter writes the backup an act was asked for (APP.md §13), from the
// Unlocked the act proved, AFTER its change and its retirement, and only once
// the retirement has landed in full (ruled 2026-10-27, R-C): the export of
// FORMAT §15 at the vault's new generation, then lastExportAt with that
// generation and the recovery keys it holds. The change stands whatever
// happens here, so a failure is the act's BackupError, not its error, and the
// act still ends Done. "" takes no backup; a lock that landed before it is
// admitted skips it; a retirement still owed skips it with BackupError
// vault.retire_owed, the stamp unchanged; once admitted it runs to its end.
func (c *Core) backUpAfter(cer *ceremony, unl *keystore.Unlocked, path string)
```

Body: "" returns. **Admission**, under `c.mu` and released before anything else: unless
`c.vault.state == StateUnlocked && c.vault.ks == unl.Keystore()`, log "backup skipped: a lock
landed after the change", release and return (the user is gone; nothing is written behind a Locked
state, and the backups card asks again at the next unlock). Then, under the same hold, while
`unl.Keystore().RetireOwed()`: log "backup skipped: the retirement did not land", release,
`cer.set` `BackupError = vault.retire_owed`, and return. The stamp is unchanged, the end says why
(3.6), and the user exports from Backups and copies once the retirement has landed, which the
export's own barrier then requires as well (2.4). This is the order ruling 1 set — the change, its
retirement, then the backup — kept where the retirement fails (the outside review's second
reading, finding 8: revision 3's helper checked only the state and the handle, and so exported and
stamped a backup while a retirement was owed). With the mutex released — `cer.set`
takes it (ceremony.go:334-342) and so does `recordExport` (keys.go:928-936), so neither is ever
called with it held (outside review, gaps) — `cer.set` Phase "backup"; `unl.Export(path)`; on
error, `cer.set` `BackupError = classify(err).Code` and log it; on success
`c.recordExport(unl.Keystore().Generation(), recoveryHex(unl.Keystore().Slots()))` and `cer.set`
`BackedUp = true`. An export admitted before a lock lands runs to its end and is recorded: it
writes a separate file from the handle the ceremony still owns, and the lock's close of the handle
waits for the ceremony (session.go:612-616). The ceremony's cancel is not consulted after the
change: the change has landed, and a Cancel pressed during any commit already ends Done today,
`cer.check()` being asked before the keystore call and never after it (keys.go:855-861, 874-877).
`Export` (mutate.go:814-841) works at the new generation and carries every `vmk_history` record,
so backups made earlier stay readable by this vault.

Every act that re-keys ticks its backup whenever a recovery key remains after it (critique 2,
UX 1): a backup made before the re-key is at the key the act retires, which the backups card then
calls *behind* (`VaultRekeyed`, below; 3.7), and adopting it would continue at that key. A
recovery key's removal ticks it on either answer, and a replacement always: one made afterwards
holds only the recovery keys then enrolled (`Export` copies the active recovery slots,
mutate.go:818-824), while the backups made while a removed or replaced key was enrolled hold it
(3.1, 3.3). The other acts' "kept" answer ticks it while a backup is owed, as the Re-key dialog
does today.

**The facts the backups card and the import need**:

- `lastExport` (internal/app/settings.go:15-18) gains `Gen uint64` (`json:"gen,omitempty"`), the
  VMK generation exported, and `Recovery []string` (`json:"recovery,omitempty"`), the hex
  recipient IDs of the recovery slots the backup holds; `recordExport(gen uint64, recovery
  []string)` (keys.go:928-936) writes both, and `ExportBackup` passes them as `backUpAfter` does.
  A stamp without them (written before this change) makes no claim about either. Neither is
  secret: both are plaintext in the vault file (FORMAT.md:932, 1006).
- `cacheFactsLocked` (session.go:180-197) caches `c.vault.generation = ks.Generation()`.
- `BackupStatus` (internal/app/types.go:102-107) gains `VaultRekeyed bool`
  (`json:"vaultRekeyed"`): the vault was re-keyed after the last backup recorded here (the stamp's
  `Gen` not zero and below `c.vault.generation`); and `KeysReplaced bool` (`json:"keysReplaced"`):
  none of the recovery keys that backup holds is still one of this vault's (the stamp's
  `Recovery` not empty and disjoint from the active recovery slots' IDs). Read, like the counts,
  only while Unlocked and not Tampered (keys.go:976-983). A backup at a retired key continues at
  that key when adopted (`AddFirstWayIn`, mutate.go:235-294; APP.md:231-241), which a removed
  credential may have reached through an earlier copy; one whose recovery keys were all replaced
  opens only with keys the user was told to destroy. The card asks for a new one in both cases
  (3.3, 3.7).
- `FileInfo` (types.go:548-566) gains `Rekeyed bool` (`json:"rekeyed"`): a file of this vault
  (`VaultMatches`) at a generation below the vault's own. `InspectFile`
  (internal/app/vaultfile.go:121-152) sets it beside `Newer` (150), from the file's plaintext
  generation, which informs a dialog and decides nothing, as `Newer` does. The import dialog then
  says it (3.7).

### 2.4 (d) The Session: one landing for every act, the unlock's finishing, and the barrier

`rotateWith` (keys.go:873-903) becomes `landChange`, used by every act that writes the slot
region (the enrolment included, so the steady state is a retired file):

```go
// landChange runs an act's commit, then R41's retirement in full, then brings
// the core onto what was committed: the next Session after a commit that
// advanced the generation (APP.md §2.1), the cached facts otherwise. A lock
// that lands after the commit leaves the act done: the change is on disk, its
// retirement still runs, and new keys are zeroed rather than installed behind
// a Locked state.
func (c *Core) landChange(cer *ceremony, unl *keystore.Unlocked, rekeys bool, commit func() error) error
```

Body, in order:

1. `cer.check()`: the last point at which a cancel or a lock changes nothing.
2. `cer.set` Step `StepDeriving`, Phase "commit".
3. The test seam `c.seams.failAt("commit")` when set; then `commit()`. On an error:
   - When it is neither `ErrIndeterminate` nor `ErrConflict` and the handle is now owed
     (`unl.Keystore().RetireOwed()`: a commit that wrote anything before it failed left the file
     owed, 2.2.3 rule 5), `c.seams.failAt("retire")` when set, else `unl.Retire()`, at once, as
     step 5 would after a landed change (critique 3, finding 5). The ceremony still holds the
     handle, and what the failed commit wrote is not left in the file for a later gated mutation
     or the next unlock to find: a replacement's new recovery slots wrapping the VMK it drew, with
     a registry sealed under that VMK's Metadata key after a failure at the fsync, while the page
     tells the user to destroy sheets that, with this file and a hand-written tool, would still
     yield every archive key; or a failed enrolment's record wrapping the current VMK, for a
     YubiKey the user may then discard. That `Retire` changes no state, so "Nothing was changed."
     stays true. An `ErrIndeterminate` or `ErrConflict` from it is returned in place of the act's
     error, so that the act enters Broken (the page reads it as unknown, and the reopen settles
     it, 3.3); any other is logged ("a failed commit's bytes could not be retired"), sets
     `retireFailed` and the warning under `c.mu`, and the act's own error stands.
   - Then, under `c.mu`, `c.vault.retireOwed = unl.Keystore().RetireOwed()` (the barrier must see
     what is still owed at once, the outside review's finding 5), and the error, through
     `vaultErr` (2.6), is returned.
4. The test seam `c.seams.committed`, "between the change and its retirement", when set.
5. `c.seams.failAt("retire")` when set, else `unl.Retire()` (2.2.3: every retiring commit the file
   needs and their scrubs, not one pass). `ErrIndeterminate` or `ErrConflict` is returned, so that
   the act enters Broken (ceremony.go:262-268). Any other error: log "the change landed; its
   retirement did not", `cer.set` `RetireError = classify(err).Code` and `RetireRemnant =
   unl.Keystore().RetireRemnantOnly()` (2.5), and under `c.mu`
   `c.vault.retireFailed = true` and the warning set; then go on: the change stands. The vault
   stays open for reading and for the files inside its archives, and every mutation that writes
   a record or a secret, or a backup, waits behind the barrier (below). It runs whether or not a
   lock landed after step 3: the handle is still the ceremony's, since the lock's close waits for
   it (session.go:612-616).
6. Without `rekeys`: `c.afterMutation()` (keys.go:342-350), whose `cacheFactsLocked` now also
   caches `retireOwed` (below); then step 8.
7. With `rekeys`: the seam `c.seams.rekeyed`, when set; `next, err := unl.Session()`, or the
   error `c.seams.failAt("session")` returns when set. On an error — reachable only through
   `current()` (unlock.go:250-261), and not expected after the handle's own commit — the act still
   ends Done (critique 3, UX M4 and format nit 9): log "re-keyed; the next Session could not be
   derived", lock the vault as an `ErrStale` reaching a service does (APP.md:95-96: `go
   c.LockNow(ReasonStale)`, as session.go:366 does, its close waiting for this ceremony), and go to
   step 8. Otherwise, under `c.mu`: if `v.state != StateUnlocked || v.ks != unl.Keystore()`, then
   `next.Lock()`, log "re-keyed; a lock landed before the session was installed", unlock and go
   to step 8, which **returns nil** (today it returns `vault.needs_unlock`, keys.go:884-892, which
   ends the act Failed with the rotation on disk); otherwise install `next`, lock the old Session,
   `cacheFactsLocked(v.ks)`, `dropPendingLocked()`, `armTimersLocked()` (which never extends a
   running idle deadline, session.go:678-683); unlock; `emitState()`; then step 8.
8. `cer.set` `After` from `afterFactsOf(unl.Keystore())` (2.5: the committed recovery IDs, the
   switch, setup needed, read from the handle's plaintext slots and header, which the ceremony
   still owns whether or not a lock landed), so that the end's next step reads what the act left
   on disk and never the page's slot list of before (R-C, finding 7); return nil.

**Once `commit()` has returned nil, `landChange` returns nil or one of the two codes that enter
Broken** (`vault.indeterminate` and `vault.conflict`, from the retirement of step 5), which the
page reads as unknown (3.5). It never returns an ordinary error after a landed change: the page
reads one as "Nothing was changed." and, for a replacement, as keys never added, which would tell
the user to destroy the vault's own recovery keys (critique 3, UX M4).

Why nil after a landed lock: `finish` then writes Done (ceremony.go:320-325), which is what is
true; no receipt is paid behind a Locked state (`applyOwedLocked` only while Unlocked,
ceremony.go:314-316); `afterLock` waits for the ceremony (session.go:612-616) and then caches the
committed facts from the handle (622-627), `retireOwed` among them, so the next unlock opens at the
new generation and knows what is owed. Between the commit and the install, `sessionLocked`
answers `ceremony.in_progress` to every other caller (session.go:355-361), so nothing writes under
the stale keys.

**The retirement finished at unlock.** `publishUnlockedLocked` (session.go:806-855), the one
place that publishes the vault — the unlock (ceremony.go:1172), the create's entry
(keys.go:1150) and *Finish setting up* (vaultfile.go:702) — gains, before `unl.Session()` and so
before the Session is installed and before `settleAfterUnlockLocked` writes any receipt or purge
(857-863):

```go
// finishRetireLocked finishes the retirement a change left owed — its own
// retirement failed, the process stopped between the two commits or during a
// scrub, or the file was written before R41 — before the vault is used
// (FORMAT R41, ruled 2026-10-27). On a tampered region it retires what
// copies nothing forward and leaves only a live copy's own tail owed (R25,
// 2.2.3). Caller holds the state mutex.
func (c *Core) finishRetireLocked(unl *keystore.Unlocked) error
```

Body: nothing unless `unl.Keystore().RetireOwed()`; then `c.seams.failAt("retire")` when set, else
`unl.Retire()`; then `c.vault.retireOwed = unl.Keystore().RetireOwed()`. On success
`c.vault.retireFailed = false`, the warning deleted, and log "finished a retirement left owed"; on
`ErrIndeterminate` or `ErrConflict` return it; on `ErrTampered` (a tampered region, retired but for
a live copy's own tail, 2.2.3) `retireFailed = false` and the warning deleted, since the Tampered
banner says what is true and every held act is refused with `vault.tampered` (critique 3, N8), and
nothing more: the vault opens Tampered and owed, and the barrier refuses what it holds (below); on
any other error log it, set `retireFailed` and the warning, and return nil. `publishUnlockedLocked`
therefore returns an error (it returns nothing today): on one of the two, it sets `v.ks = ks` (so
that *Reopen* closes it), closes the `Unlocked`, enters Broken through `brokenLocked` and returns
the error; the unlock and *Finish setting up* return it, so that their ceremony ends Failed with
`vault.indeterminate` or `vault.conflict` over the Broken state, and the create's entry, which opens
a new file that owes nothing (2.2.3), logs it. On success it goes on as today. A legacy vault, owed
because no writer retired before this change, is retired at its first unlock here, silently.

**A retirement that cannot be finished: the vault stays readable, and the barrier holds every
mutation that writes a record or a secret** (ruled 2026-10-27, R-A, answering section 6's first
question of revision 3). A retirement that fails at unlock in any other way, or at the act (step 5
above), leaves the vault Unlocked with the warning `vault.retire_owed`: its archives open, their
files can be read and, inside an existing archive, added, replaced, renamed, moved and deleted,
and nothing that writes a record or a secret into the vault file, or a backup of it, lands until
the retirement has landed in full. Each such mutation first tries to finish it. Unknown outcomes
stay Broken.

*Why archive content is not held* (R-A). Writing files into an existing archive changes no key
and adds no record: the archive's own key is not changed by a VMK rotation (FORMAT.md:1278-1280),
so content written while a retirement is owed is open to an earlier copy of the vault file
exactly as content written after it lands is, and the limit every dialog states — "an archive that
existed then stays open to it, files added later included, until that archive is re-keyed" (3.1,
item 7) — covers both. What a content write does put in the registry is verified below: the
receipt, which is neither a record nor a secret.

**The verification R-A asks for: every operation, and what it writes into the registry.** Every
registry write the app makes goes through `updateRegistryAtLocked` (session.go:398-442) into
`Session.UpdateRegistryAt` and `updateRegistryAt` (unlock.go:386-399), a commit with no slots
and no header. Its callers outside the tests are fifteen: ops.go:1544, 1607, 2104, 2223, 2264,
2294; records.go:38, 83, 206, 236, 367; merge.go:434; session.go:890, 929; settings.go:279 (found
by a search for the four entry points `updateRegistry(`, `updateRegistryLocked(`,
`updateRegistryAt(` and `updateRegistryAtLocked(`; each read at b79a10c). The receipt
(`recordReceiptLocked`, ops.go:1542-1569, the first of them) is in turn called from six places:
archives.go:845 and 1385, ops.go:1728, 2032, 2139 and 2150. The keystore's own writes are the
`Unlocked`'s, through the ceremonies of keys.go and vaultfile.go. By what each writes:

| Operation (where) | What it writes into the vault file's registry | Held by the barrier |
| --- | --- | --- |
| Add files, add a folder (`AddFiles`, `AddFolder`, ops.go:585, 624; op "add", 597; its transaction, 763) and replace a file (`ReplaceFile`, ops.go:1083; op "replace", 1095; 1124) | The archive file, through its transaction; then the receipt (`opTx.commit`, archives.go:1360-1394, into `recordReceiptLocked`): the existing record's `last_stored_size`, `last_written_at`, `last_seq`, `revision` and `last_writer` | No: content |
| New folder, delete, rename and move inside an archive (`CreateFolder`, `DeleteRecords`, `RenameRecord`, `MoveRecords`, archives.go:1592, 1648, 1695, 1756; transactions at 1627, 1676, 1732, 1824) | The same receipt | No: content |
| Verify (`Verify`, ops.go:1572; write 1607) | `last_ciphertext_hash`, `hash_at_seq`, `last_seq`, `last_stored_size`; it may rewrite the archive's own envelope (1595-1599) | No: bookkeeping |
| Compact and the reclaim (`Compact`, ops.go:1633, 1672; receipt 1728; `startReclaim`, 1903; receipt 2032) | The archive rewritten under the key it has; the receipt, with the hash after a compaction | No: content |
| The receipt adopted at open (archives.go:845) and the receipts a lock stranded, paid at unlock (`applyOwedLocked`, session.go:923; write 929) | The same receipt fields | No: bookkeeping |
| Rename an archive (`RenameArchive`, records.go:30; write 38) and its description (`SetArchiveDescription`, records.go:75; write 83) | `name` or `description`, `revision`, `last_writer` of an existing record | No: a record's field, neither a record added, removed or forgotten nor a secret |
| Hide, show (`HideArchive`, ops.go:2259; write 2264) and locate (`Locate`, ops.go:2286; write 2294) | The policy's hidden bit, or `last_path` | No: as above |
| The vault's lock times (`SetSettings`, settings.go:224; write 279) | `idle_minutes`, `absolute_minutes` | No: a setting |
| Create an archive (`CreateArchive`, ops.go:2166; the file at 2210, the write at 2223) | A new archive record with its wrapped archive key | **Yes**: a record and a secret |
| Re-key an archive (`RotateKey`, ops.go:2063; write 2104, before the archive adopts the key) | A new version record with a new wrapped archive key; the current one retired | **Yes**: a secret |
| Import archives from a backup (`MergeRecords`, merge.go:402; write 434) | Records added, with their version records and wrappers; forgotten records restored | **Yes**: records and secrets |
| Its inspection (`InspectRecords`, merge.go:48; begun at 55) | Nothing; it is the first half of `MergeRecords` | **Yes**, at its begin, as the act it begins (critique 3, M8) |
| Forget (`ForgetArchive`, records.go:193; write 206; `DeleteArchive` without the file is the same call, 288-290) | `forgotten_at` | **Yes**: named by R-A |
| Delete with the file (`DeleteArchive`, records.go:287; the file removed at 346, the write at 367) | `forgotten_at`, after the file goes | **Yes**: named by R-A |
| Restore (`RestoreArchive`, records.go:231; write 236) | `forgotten_at` cleared | **Yes**: a record's state, forgetting undone |
| The purge at unlock (`purgeForgottenLocked`, session.go:871; write 890) | Records removed | **Yes**: deleting records; deferred, not refused (below) |
| Remove an unlock method, change the password, turn it on or off, re-key the vault, add an unlock method, replace the recovery keys (keys.go, through the `Unlocked`), *Finish setting up* (`AddFirstWayIn`, vaultfile.go:655-711) | Slots, the slot region header, the registry's secrets section | **Yes**: named by R-A |
| Export a backup (`ExportBackup`, keys.go:907-921) | Nothing in this file; a new file holding the recovery slots and every archive key | **Yes**: named by R-A |
| The reveal (keys.go:689-745), `VerifyBackup`, `InspectFile`, extract, previews, listings | Nothing | No: reads |

The finding: **no operation on the content of an existing archive writes a record or a secret.**
Each writes the receipt alone, and every field of it describes the archive file just written —
its superblock `seq` and its size, which whoever can open the file reads there; the hash of its
bytes; the commit's time, which the receipt takes from the clock at the commit (archive/tx.go:767,
889; whether the archive's own superblock records the same time was not traced); a revision
counter; and this device's ID, already in the registry. So R-A's claim holds as the owner stated
it, and the content operations are not gated. The rule the table applies, and the one a builder
applies to any write added later, is: **held while a record is added to the registry, removed from
it or moved between current and forgotten, while a secret is written into the vault file, and for
a backup**; not held for a field of an existing record, a vault setting or a receipt. Rename,
description, hide, locate and the lock times are outside that rule and so are not held; adding any
of them is one call switched from `updateRegistry` to `publishRegistry` (below), should the owner
want it. A read is never held, save the one that is the first half of a held act: the merge's
inspection, which the barrier holds at its begin, so that its refusal comes before the proof and
the choice of archives rather than after both (critique 3, M8).

**The gate's state, as nonsecret facts that survive Locked and Broken** (R-C, finding 5's point
for what is gated). Two fields of the vault state:

- `retireOwed bool`: `ks.RetireOwed()`, the predicate over the file's plaintext bytes (2.2.3), no
  secret in it. Cached by `cacheFactsLocked` (session.go:180-197) wherever the facts are cached —
  the open (openVaultFile, session.go:108-153), the unlock, `afterMutation`, and `afterLock`, which
  reads it from the handle it still holds before closing it (session.go:612-630) — and refreshed
  from the handle after every keystore write the core makes: `updateRegistryAtLocked` after its
  commit, failed or not (its `ErrNoChange` path writes nothing), `landChange` (steps 3, 6 and 7),
  `finishRetireLocked` and the barrier. A Busy open sets it false (nothing is known); an open of
  another vault clears `retireFailed` with the rest of that vault's state. `Status()` carries it
  as `VaultStatus.RetireOwed` (`json:"retireOwed"`), so that the import's confirmation can say
  what the dated copy it keeps still opens with (below).
- `retireFailed bool`: an attempt to finish the retirement failed. Not cleared by a lock
  (`lockLocked` clears the Session, the handle and the Tampered verdict, session.go:555-605) nor
  by Broken (`brokenLocked`, 481-490); cleared when a retirement lands, whoever lands it:
  `updateRegistryAtLocked`, after a commit that leaves nothing owed (a registry write's carried
  pass finishes a file this writer made, 2.2.3 rule 4), clears it and the warning, as the barrier,
  `landChange` and `finishRetireLocked` do. It is cleared with the warning on the Tampered paths
  too (`finishRetireLocked` and the barrier's step 8 meeting `ErrTampered`), where the Tampered
  banner says what is true and every held act is refused with `vault.tampered`, so that the
  warning's promise of a retry at each act never stands beside it (critique 3, N8).

`cacheFactsLocked` sets the warning `vault.retire_owed` from `retireFailed && retireOwed`, and
deletes it otherwise: a retirement merely owed — a legacy file, or between a change and its
retirement — warns nothing, since the next unlock or the next gated mutation finishes it; one that
was attempted and failed warns until it is finished, on the lock screen too. While Locked or
Broken nothing gated can run (each needs a Session, and `sessionLocked` answers
`vault.needs_unlock` or `vault.broken` first, session.go:342-372), so the facts are what the lock
screen says and what the next unlock acts on; at *Reopen* the file's own bytes decide again
(`load`). The open archives that stay writable across a lock (APP.md:624) write content and owe
their receipts, as today, and are not held (above).

**The barrier:**

```go
// retireBarrierLocked holds every mutation that writes a record or a secret
// into the vault file, or a backup of it, behind an owed retirement (FORMAT
// R41; ruled 2026-10-27, R-A): it first finishes the retirement in full —
// every retiring commit the file needs and their scrubs — and refuses while it
// cannot. It never reads a handle a ceremony holds, and otherwise reads it
// under the state mutex, so it is valid in every state. Caller holds the
// state mutex.
func (c *Core) retireBarrierLocked() *Error
```

Body, in order:

1. While a mutation ceremony or a pending touch holds the handle (`c.cer != nil &&
   c.cer.mutation`, `c.pending != nil && c.pending.vaultHandle`), the handle is the ceremony's and
   is not read: the Keystore is not safe for concurrent use, and a ceremony writes its retirement
   state from its own goroutine outside `c.mu` (2.2.3, rules 5 and 7; `landChange` step 5). This
   comes first (critique 3, finding 4): `CreateArchive` meets the barrier at its Session check
   (ops.go:2192-2197), and `sessionLocked` answers with a Session while a ceremony runs, save
   during a rotation's install (session.go:342-372), so a create during a removal would otherwise
   read the handle unsynchronised. Return `ceremony.in_progress`, whatever the cached
   `c.vault.retireOwed` says: as built, a held write let through when it said nothing owed read
   the Session, and the handle with it, before its registry write met `updateRegistryAtLocked`'s
   own refusal, which now also comes before that write's Session is read (the outside review of
   the diff, finding 5). `beginAs`, which begins an act over a pending touch that its ceremony
   adopts or waits for, refuses on the cached fact there and otherwise meets this barrier
   (`actBarrierLocked`). This is also what keeps `Session.Retire` from racing a ceremony's own
   commits (the outside review's finding 2, its last point).
2. `owed := c.vault.retireOwed`; when `c.vault.ks != nil`, `owed = c.vault.ks.RetireOwed()` (the
   handle is the truth while there is one, and no ceremony holds it, step 1). Not owed: return
   nil, writing nothing.
3. `sess, e := c.sessionLocked()`; `e` is returned (`vault.needs_unlock`, `vault.broken`,
   `vault.none`, or `ceremony.in_progress` while a rotation installs its Session).
4. `err := c.seams.failAt("retire")` when set, else `sess.Retire()`.
5. `c.vault.retireOwed = c.vault.ks.RetireOwed()`; `c.vault.modifiedAt` and `vaultFileSize` from
   the handle, as `updateRegistryAtLocked` sets them (session.go:438-439).
6. `err == nil` and nothing owed: `retireFailed = false`, the warning deleted, log "finished a
   retirement left owed", return nil.
7. `ErrIndeterminate` or `ErrConflict`: `brokenLocked(err)`, return `classify(err)`.
8. `ErrTampered` (the live region fails R25, and everything that copies nothing forward has been
   retired, leaving a live copy's own tail, 2.2.3): `retireFailed = false` and the warning
   deleted (critique 3, N8); return `vault.tampered` (R-C, finding 4: refused until an import
   establishes a clean state; the region is never copied forward). The Tampered banner is what
   the user reads, and its remedy is the cure.
9. Anything else, or `nil` with something still owed: `retireFailed = true`, the warning set, log
   it, return `vault.retire_owed`.

Holding the state mutex across `Session.Retire` — a 128 KiB region write, a registry write, the
scrub and their fsyncs, twice for a legacy file — is what `updateRegistryAtLocked` already does for
every registry commit, "so a software lock cannot land between" (session.go:374-381); it is paid
once, when a gated mutation meets an owed retirement.

```go
// publishRegistryAtLocked is updateRegistryAtLocked for a write the barrier
// holds (a record added, removed or moved between current and forgotten, or a
// secret written): the barrier, then the write, under one hold of the state
// mutex, so that the retirement lands before this write's modified_at is
// computed (the outside review's finding 3). Caller holds the state mutex.
func (c *Core) publishRegistryAtLocked(allowForgotten bool, fn func(g *registry, modifiedAt int64) error) *Error
func (c *Core) publishRegistryLocked(fn func(g *registry) error) *Error
// publishRegistry and publishRegistryAt take the state mutex themselves, as
// updateRegistry and updateRegistryAt do (session.go:378-382, records.go:259-263).
func (c *Core) publishRegistry(fn func(g *registry) error) *Error
func (c *Core) publishRegistryAt(allowForgotten bool, fn func(g *registry, modifiedAt int64) error) *Error
```

Where each held mutation meets the barrier. A refusal comes before anything is written, and the
barrier is met again at the registry write under the same hold as the write, so that a retirement
left owed in between (a ceremony that landed meanwhile and whose retirement failed) is never
written over:

- `CreateArchive`: at its Session check (ops.go:2192-2197), before the archive file is created;
  and its registry write (2223) becomes `publishRegistry`, whose refusal removes the new file as
  every failure there does (`os.Remove(p)`, 2231). As built, the key is wrapped inside that write,
  under the Session it commits with, and not at the Session check: a re-key can land while the
  file is made (the outside review of the diff, finding 3).
- `RotateKey`: at its synchronous Session check (ops.go:2071-2077), so a refusal starts no
  operation; and its registry write (2104) becomes `publishRegistryLocked`. Do not let it proceed
  because a pass committed: the barrier returns nil only with nothing owed (finding 3). As built,
  the barrier is met first, before anything reads the vault, with the forgotten check in the same
  hold of the state mutex: that check reads the registry through the Session and the open of a
  closed archive unwraps its key through it, while a ceremony commits through the same handle,
  so a refusal opens no archive either; then again ahead of the synchronous check's Session
  read; and the key is wrapped inside the registry write, as `CreateArchive`'s is (the outside
  review of the diff, finding 3; the review of those repairs, finding A).
- `MergeRecords`: its write (merge.go:434) becomes `publishRegistryAtLocked(true, …)`; it holds
  the state mutex from its checks to its write (merge.go:403-404).
- `ForgetArchive`: the barrier under the state mutex before `closeForRecord` (records.go:200), so
  that a refused Forget neither closes the archive nor cancels its operation; its write (206)
  becomes `publishRegistryAtLocked`.
- `RestoreArchive`: its write (records.go:236) becomes `publishRegistryAt(true, …)`.
- `DeleteArchive` with the file: the barrier under the same hold of the state mutex as its
  Session check and its claim on the record (records.go:301-325), before the file is removed
  (346); its write (367) becomes `publishRegistryAt`. While a Delete holds its claim
  (`c.deleting` not empty), `beginAs` refuses a slot change with `op.in_progress` (below), so
  that no change and failed retirement can land between the file's removal and the record's
  forgetting. One end remains (critique 3, nit 8): between the two barriers a registry write the
  barrier does not hold — a receipt, a rename — can fail after writing (2.2.3, rule 5) and the
  second barrier's scrub can then fail too. The file is then gone and the record stays current:
  the archive reads as missing, as one whose file was lost does today, and *Forget archive…*
  finishes it once the retirement lands. Letting the delete's own write pass on the first
  barrier's verdict was declined: R-A names forgetting and deleting a record among what waits.
- The purge at unlock (`purgeForgottenLocked`, session.go:871): skipped while
  `c.vault.ks.RetireOwed()`, beside its Tampered skip (872-874), logged "purge deferred: a
  retirement is owed"; the records stand and the next unlock purges them. Deferred rather than
  attempted: the unlock's `finishRetireLocked` has just tried.
- The keystore acts and the export, through `beginAs` (2.5): after its refusals of state, Tampered
  (unless `allowTampered`), a running ceremony and a registry-writing operation (keys.go:190-212),
  and a new one, `op.in_progress` while `c.deleting` is not empty, `beginAs` calls the barrier,
  under the same hold of the state mutex as `newCeremonyLocked`, unless the act only reads
  (`actOpts.reads`). So no ceremony that writes the file or a backup begins over an owed
  retirement, and none begins between a barrier and its write. `beginMutation` (keys.go:179-183),
  which `ExportBackup` uses, becomes `beginAs(kind, actOpts{}, fn)`; `beginWithVMK` stays for the
  reveal (keys.go:717), as `beginAs(kind, actOpts{allowTampered: allowTampered, reads: true},
  fn)`. The merge's inspection (merge.go:55) becomes `beginAs("records", actOpts{allowTampered:
  true}, fn)`, without `reads`: it writes nothing, but it is the first half of `MergeRecords`, so
  the barrier refuses it at its begin, before the proof (a recovery key's 48 digits, where the
  file is another copy's) and the choice of archives, rather than at the merge's write after both
  (critique 3, M8). MergeDialog's `read` already puts a refusal of `Vault.InspectRecords` in its
  error notice (MergeDialog.svelte:55-56, 110-113). On a Tampered vault that owes nothing it runs
  as today (merge.go:43-47).
- *Finish setting up* (vaultfile.go:655-711) runs while Locked, with no Session: inside its
  ceremony, once its recovery key has given it an `Unlocked` and before `AddFirstWayIn`, while
  `unl.Keystore().RetireOwed()`, `unl.Retire()` (or the seam): `ErrIndeterminate` and
  `ErrConflict` enter Broken; any other error ends the act Failed with `vault.retire_owed`, nothing
  of it written. Its own change is then retired by `publishUnlockedLocked`'s `finishRetireLocked`.

**A Tampered vault that owes a retirement** (R-C, finding 4; critique 3, finding 3). It stays
readable, and the content of its archives changes as on any Tampered vault. Its retirement is
finished as far as R25 lets it be, by `Retire`'s Tampered branch (2.2.3): a registry-only commit
clears the losing superblock copy, zeros clear the slot region copy neither superblock copy
names, the scrub clears the registry area, and none of it copies the damaged region forward.
That is the repair R-C allows beside an import. It runs at the unlock (`finishRetireLocked`) and
at the barrier, and once nothing is owed the vault behaves as a Tampered vault that owes nothing:
the keystore acts refused by `vault.tampered` at `beginAs` as today (keys.go:197-200), the
registry mutations and the export admitted as today. What it cannot clear is a live copy's own
tail (clause 1), which only a writer before this change leaves and only a region write could
remove. While that remains, every mutation the barrier holds is refused until an import of a
copy establishes a clean state: the keystore acts by `vault.tampered` at `beginAs`, and the
registry and backup mutations by the barrier's step 8, `vault.tampered`, whose words already say
what is true and what to do ("This vault file does not match itself, so changes to it are off.
Replace it with a copy you trust.", strings.ts:216). The verdict is stable: no registry-only
commit touches the live region copy, so a receipt that lands meanwhile (a plain commit on a
Tampered region, 2.2.3 rule 4) cannot turn a refusal into an admission, as it could under
revision 4's refusal of every clause, where a receipt cleared clause 3 by its own superblock
write. A Tampered vault that owes nothing behaves as today. Revision 3's exception — the gate
standing aside on a Tampered vault, which its test 18 pinned — stays struck.

**The import keeps a dated copy of the vault it replaces** (critique 3, finding 2). Every import
over a kept vault goes through `install`, which first `retire`s the file at the vault's place as
a whole-file copy, `vault-replaced-<unix>-<n>.eks` in the data folder (vaultfile.go:258-295,
308-321), "never read by Enfold again ... their ways in stay valid for them" (APP.md:184-185). A
vault that still owes a retirement — one whose retirement kept failing, a Tampered vault's live
tail, a legacy file never unlocked here — is copied whole, so the copy holds what R41 exists to
remove: after a removal without a re-key, the withdrawn record wrapping the current VMK; after a
re-key, the losing superblock and the registry the retiring VMK reads. The import runs from
Locked or from no vault (`importGateLocked`, vaultfile.go:414-438), so the core holds no key
there, but the cached `retireOwed` survives Locked and is read from the file's bytes. So
`VaultStatus.RetireOwed` carries it, and the import's confirmation, while it is true, adds after
its sentence that the replaced vault's own unlock methods still open it
(ImportDialog.svelte:185-190): "It also still holds its state from before its last change, so it
opens with any unlock method that change removed too. Delete it once the new vault opens."
(`importCopy.replacedOwes`, 3.7). The sentence reads true of the file "Use the file where it is"
leaves where it is as well. The create needs no such words: it runs only with no vault kept or
over a damaged one (keys.go:1034-1039), of which the core holds no facts. Considered and
declined, the critique's option: finishing the parts that need no key before `retire` copies an
owed vault, the scrub and zeros over an unnamed region copy. The owed states that matter, a
change's other region copy and its losing superblock, need the Metadata key, which a Locked
import does not hold, so the copy would still hold them and the confirmation would still have to
say so.

The acts' `fn`, each begun through `beginAs(kind, o, fn)` (2.5):

- `RotateNow(backupPath)`, kind "rotate": `landChange(cer, unl, true, unl.Rotate)`, then
  `backUpAfter`.
- `RemoveSlot(id, rekey, backupPath)`, kind "remove", `without` the ID when `rekey` and the slot
  is a YubiKey (2.3): `landChange(cer, unl, rekey, f)` with `f` calling
  `unl.RemoveSlotAndRotate(rid)` or `unl.RemoveSlot(rid)`; then `backUpAfter`.
- `ChangeEntangledPassword(rekey, backupPath)`, kind "password" (2.5): `cer.releaseCard()`;
  `cer.askNew("password", StepPassword)`; `landChange(cer, unl, rekey, f)` with `f` calling
  `unl.ChangeEntangledPasswordAndRotate(e)` or `unl.ChangeEntangledPassword(e)`; then
  `backUpAfter`.
- `SetEntangled(on, rekey, backupPath)`, kind "entangle": `cer.releaseCard()`; on, `cer.askNew`
  as now (keys.go:88-93); `landChange(cer, unl, on && rekey, f)` with `f` calling
  `unl.SetEntangledAndRotate(*e)` or `unl.SetEntangled(on, e)`; then `backUpAfter` (on only).
- `BeginEnroll(o)`, kind "enroll", `recoveryProof` from `o.WithRecoveryKey`: as today
  (keys.go:367-424), each `unl.AddSlot(spec)` and its `c.afterMutation()` becoming
  `landChange(cer, unl, false, func() error { return unl.AddSlot(spec) })`; a recovery key is
  minted and shown after it, as now.
- `ReplaceRecoveryKeys(backupPath)`, kind "replace": 2.7.
- `ExportBackup(path)`, kind "export", through `beginMutation` and so the barrier (above), gains
  only `cer.set` Phase "backup" before `unl.Export` and the two new arguments of `recordExport`
  (keys.go:907-921), so its panel says what it does.
- `FinishSetup(first)` gains its refusal (2.3) after `setupNeededLocked()`, under the same hold of
  `c.mu`: `if c.vault.entangled { return coded(CodeSetupPasswordOn) }`; and the retirement before
  `AddFirstWayIn` (above).

The new password is typed before anything is committed and no backup is written before the
change, so a failed act never costs a typed password and never leaves a file behind.

### 2.5 (e) Events, the kinds, the steps, and the done line

`CeremonyState` (internal/app/types.go:146-173) gains these fields, and its `Kind` comment (148)
gains `password` and `replace`:

```go
	// Rekey: the act re-keys the vault in the same commit as its own change
	// (a removal, a password change, turning the password on, a replacement),
	// from its first event on, so that the panel's end says both. False for
	// kind rotate, which is the re-key itself.
	Rekey bool `json:"rekey"`
	// Phase, once the proof has passed: "commit" while the act's change and
	// the commit that retires the losing copy are written, "backup" while
	// the backup it was asked for is (APP.md §13). Empty during the proof.
	Phase string `json:"phase,omitempty"`
	// Without: the hex recipient ID of the YubiKey a re-keying removal takes
	// away, which its proof does not ask for: the wait's line leaves it out,
	// and it does not count as a YubiKey to prove with. Empty for every other
	// act, a password's removal included (the password removed proves it).
	Without string `json:"without,omitempty"`
	// BackedUp: the backup the act was asked for is written, after its
	// change; the page shows the export's done line when the dialog closes.
	BackedUp bool `json:"backedUp"`
	// BackupError: why that backup was not written, the change standing;
	// vault.retire_owed when it was not taken because the act's retirement
	// did not land (2.3); the act still ends Done, and its end says so.
	BackupError Code `json:"backupError,omitempty"`
	// RetireError: why the retirement of the file's earlier state did not
	// land, the change standing (FORMAT R41); the act still ends Done, its
	// end says so, vault.retire_owed stands, and nothing that writes a
	// record or a secret, and no backup, lands until it does (the barrier,
	// 2.4), each such mutation and the next unlock trying to finish it first.
	RetireError Code `json:"retireError,omitempty"`
	// RetireRemnant, with RetireError: what the retirement left is earlier
	// registry ciphertext alone, in space no superblock names (FORMAT R41's
	// fourth clause): no copy of the file opens with the method the act
	// withdrew, and the end's bar says so in its own words (3.6).
	RetireRemnant bool `json:"retireRemnant,omitempty"`
	// KeepIndex and KeepCount, at StepKeepKey: which of the new recovery
	// keys a replacement shows (from 1) and how many it makes.
	KeepIndex int `json:"keepIndex"`
	KeepCount int `json:"keepCount"`
	// OldIDs, from a replacement's first StepKeepKey on: the IDs of the
	// recovery keys it replaces, in the order the new ones are shown (the
	// new key at KeepIndex replaces OldIDs[KeepIndex-1]). NewIDs: the new
	// keys' IDs, each from the step that shows it; both stand in the final
	// state, the receipt of the end (FORMAT.md §18.4).
	OldIDs []string `json:"oldIds,omitempty"`
	NewIDs []string `json:"newIds,omitempty"`
	// After, from Done on: what the act left committed, read from the
	// handle's plaintext slots and header once its commits have landed
	// (2.4, landChange step 8), so that the end's next step is decided by the vault
	// as it now is and never by a slot list the page read before (the
	// outside review's second reading, finding 7). Nil for the acts that
	// write no slot (the reveal, the inspection, an export).
	After *AfterFacts `json:"after,omitempty"`
```

```go
// AfterFacts is what an act left committed, for its end's next step (3.6).
type AfterFacts struct {
	RecoveryIDs []string `json:"recoveryIds"` // the active recovery slots' IDs, as the cards show them (recoveryID, core.go:336-339)
	Entangled   bool     `json:"entangled"`   // the slot region header's switch
	SetupNeeded bool     `json:"setupNeeded"` // every active method a recovery key (setupNeededLocked, vaultfile.go:200-210)
}
```

Two steps join `CeremonyStep` (types.go:126-142): `StepKeepKey CeremonyStep = "keep_key"` — a
new recovery key is shown, its one-time URL in `SlotLabel` and its ID in `RecoveryID`, and the
prompt of kind `kept` waits for the user to have kept it — and `StepConfirm CeremonyStep =
"confirm"` — the prompt of kind `confirm` waits for the replacement's one confirmation before
its commit (3.3; R-B). The `prompt.kind` comment (ceremony.go:75) gains `kept` and `confirm`;
both are answered through `SubmitSecret` with an empty value, the one mailbox a ceremony is
answered by (ceremony.go:200-224: the kind and the id must match, so neither can answer a PIN's
prompt), and the shell's comment on the raw channel (main_windows.go:316-317) says it carries
those two answers besides the four secrets. Why not a bound call: the answer is "the prompt with
this id may go on", which is exactly what the mailbox is, and a second path to a ceremony would
need its own stale-id rule.

`beginWithVMK`'s body becomes `beginAs(kind string, o actOpts, fn)`:

```go
// actOpts is how an act begins: whether it may run on a tampered vault (the
// reveal), whether it re-keys in the same commit as its change, the YubiKey a
// re-keying removal does not ask for, and whether the proof is a recovery key
// by the user's word (ruled 2026-10-27).
type actOpts struct {
	allowTampered bool
	rekey         bool
	without       [16]byte
	recoveryProof bool
	// reads: the act writes nothing into the vault file and saves no
	// backup (the reveal), so the barrier does not hold it. The merge's
	// inspection writes nothing either but is the first half of a held act,
	// and does not set it (2.4).
	reads bool
}
```

It sets `cer.state.Rekey`, `cer.without`, `cer.state.Without` and `cer.recoveryProof` right after
`newCeremonyLocked(kind)` (keys.go:213), before the first emit (215). Before that, under the same
hold of the state mutex, it refuses `op.in_progress` while a Delete holds its claim (`c.deleting`
not empty) and, unless `reads`, calls the barrier (2.4). `beginWithVMK(kind, allowTampered, fn)`
stays as a one-line call of it with `reads` set, for its caller the reveal (keys.go:717); the
merge's inspection (internal/app/merge.go:55) calls `beginAs("records", actOpts{allowTampered:
true}, fn)` instead, so that the barrier holds it (2.4); `beginMutation` (keys.go:179-183), which
`ExportBackup` and today's slot acts use, becomes `beginAs(kind, actOpts{}, fn)`.

The password change's ceremony kind becomes **"password"** (it is "entangle", keys.go:122;
`SetEntangled` keeps "entangle", keys.go:82): the change is its own act, with a question and an
end of its own, "Password changed", which kind "entangle" cannot say without also saying it for
*Turn on…* and *Turn off…*; today all three end on the generic "Done", there being no entangle
entry in `doneTitle` (strings.ts:173-178). The panel's two kind checks for the vault's chosen
password (CeremonyPanel.svelte:96, 104) and its `proves` list (130) take the new kinds (3.6).

Events keep their shape: each act emits `vault.ceremony` states of its kind (`Rekey` and
`Without` from the first, `Phase` as it moves, `BackedUp`, `BackupError` or `RetireError` once
known), then `finish` emits the final `vault.ceremony` and `vault.state` (ceremony.go:326-330);
`landChange` emits `vault.state` after the install, as `rotateWith` does. There are no op events:
ops are the archives' (APP.md:2420-2422).

The done line is the dialog's end, its title over the check (APP.md:3264), by kind and `Rekey`:

| Act | Kind | Rekey | Done title |
| --- | --- | --- | --- |
| Removed and re-keyed | remove | true | Unlock method removed and vault re-keyed |
| Removed alone | remove | false | Unlock method removed (as now, strings.ts:175) |
| Password changed and vault re-keyed | password | true | Password changed and vault re-keyed |
| Password changed | password | false | Password changed |
| Password turned on and vault re-keyed | entangle | true | Password turned on and vault re-keyed |
| Password turned on, or off | entangle | false | the generic "Done", as now |
| Re-keyed | rotate | false | Vault re-keyed (as now, strings.ts:176) |
| Recovery keys replaced | replace | true | Recovery keys replaced and vault re-keyed (one key: Recovery key replaced and vault re-keyed) |

With `BackupError` the end adds an attention bar under its title (3.6); with `BackedUp`, closing
the dialog shows the export's own done line in the status bar, "Backup saved to
E:\backup-2026-10-27.eks" [*Show in Explorer*], as a plain export's does (KeysPage.svelte:328-331;
APP.md:2420-2422). With `RetireError`, an attention bar says what the file still opens with and
what waits until the retirement lands (3.6).

**The status bar's done line does not carry the follow-up command.** It holds 6 s, or until the
next press in a page's body (APP.md:2392-2397; `DONE_MS`, frontend/src/lib/state.svelte.ts:38),
and its one hyperlink button is *Show in Explorer* or *Details*, by a closed union
(`DoneAction`, frontend/src/lib/foot.ts:140): a security follow-up there would be gone before it
was read. The follow-up to *Replace recovery keys…* therefore stands in the dialog's end, beside
its reason, where the user is (3.1, 3.6), and the backups card keeps saying what a replacement
leaves owed (3.3).

### 2.6 (f) Exact signatures, error codes, tests

Keystore (internal/keystore/mutate.go, file.go): the signatures of 2.2; in `commit` the
zero-filled region write, the zero-padded registry write, `txn.retire`, the carried retirement,
the scrub after the flip and `k.owed` (2.2.3, rules 1 to 7); the predicate computed in `load`
(file.go:103-142) and in `create` (file.go:458-560, where it is false: copy B of a new file is
never written and its one registry sits at `0x42000` with nothing after it). `RemoveSlot`,
`ChangeEntangledPassword`, `SetEntangled`, `Rotate`, `AddSlot` and `Export` keep their
signatures and their behaviour. The new unexported parts:

```go
// retireState is R41's predicate kept by clause, so that Retire knows whether
// a retiring commit is needed or a scrub suffices. RetireOwed is their union.
type retireState struct {
	liveTail bool // clause 1: the live region copy holds a nonzero byte past slot_region_len
	other    bool // clause 2: the other copy is neither the live region and zeros, nor zeros
	loser    bool // clause 3: the losing superblock is damaged, of another generation, or names another region
	remnant  bool // clause 4: an unnamed 4 KiB unit of the registry area holds a nonzero byte
	mixed    bool // clause 4, by a unit that also holds a byte of a named registry (legacy)
}

// Keystore gains end uint64, the file's physical end as this handle has
// written it (2.2.3, rule 8): set from the stat at load, raised to off+len
// before every registry write, failed or not, and lowered only by a trim
// that succeeded; k.size, and so FileSize(), is set to it after every flip.
// And owed retireState, and unsynced bool (rule 6): set before a trim cuts
// the file and before a scrub writes, cleared only by the scrub's fsync.

// readRetire computes retireState from the file: both region copies, the
// losing superblock's decode, and the 4 KiB units of [0x42000, k.end) not
// wholly inside a named registry extent. Called by load and after every
// retiring commit.
func (k *Keystore) readRetire(loser *format.KeystoreSuperblock) (retireState, error)

// scrub first re-reads the two superblock copies (checkOnDisk) and writes
// nothing when another writer has committed since the flip, leaving the
// handle Broken with ErrConflict. Then it writes zeros over every 4 KiB unit
// of [0x42000, k.end) that holds no byte of the live registry's extent or
// the losing superblock's and holds a nonzero byte; it leaves a unit that
// holds both (a legacy one) and reports it as mixed. Then it fsyncs whenever
// the handle owes one (unsynced): after it wrote, after a trim that cut the
// file, and after an earlier scrub whose write or fsync failed.
// It writes no superblock and no region, so it has no commit point: an I/O
// error leaves the file owed and the handle sound. Called by commit after
// its trim and by Retire.
func (k *Keystore) scrub() error

// zeroOther writes zeros over the whole slot region copy neither superblock
// copy names, then fsyncs: Retire's Tampered branch, once the losing copy
// names the live region copy (2.2.3). It refuses, writing nothing, unless
// both superblock copies name the live copy. It writes no superblock and no
// region a superblock names, so it has no commit point: an error leaves the
// file owed and the handle sound.
func (k *Keystore) zeroOther() error

// regPadEnd is where a registry written at off with n plaintext bytes stops
// its zero padding: the 4 KiB boundary after its tag, but not past k.end,
// the file's physical end before the commit, and not into the live
// registry's extent.
func (k *Keystore) regPadEnd(off, n uint64) uint64
```

Two test seams beside `now` (file.go:18):

```go
// commitFault, set by a test, is asked at each step of a commit and of a
// scrub and fails it there with what it returns: "region" (before the slot
// region is written), "registry" (after it, before the registry), "sync"
// (before the barrier's fsync), "superblock" (in place of the superblock
// write: the commit point, so the commit is indeterminate), "flipsync"
// (after the superblock write, in place of its fsync), "trim" (in place of
// the trim's truncation, which then fails as an ignored error does),
// "scrub" (at the scrub's start, before its check of the disk: the commit
// has landed), "scrubsync" (in place of the scrub's fsync, the barrier that
// makes the trim before it and the scrub's own zeros durable) and "zero"
// (before zeroOther's write). nil outside tests.
var commitFault func(step string) error

// writeSeen, set by a test, is told what ("region", "registry",
// "superblock", "scrub" or "zero"), the offset and the length of every
// write a commit, a scrub or zeroOther makes, so that a test can show that
// none touched the live slot region copy or a 4 KiB unit of a named
// registry. nil outside tests.
var writeSeen func(what string, off, n int64)

// SetCommitFault and SetWriteSeen install commitFault and writeSeen for a
// test of another package: the app's tests fail a commit, a retirement or a
// scrub, or watch their writes, through them (app tests 10, 11, 22, 29, 30,
// 32 and 33), since a package-level variable that is not exported cannot be
// set from internal/app. Each returns a function that puts the previous
// value back. Package-level, as now is: a test that sets one does not run
// in parallel. Never called outside tests.
func SetCommitFault(f func(step string) error) (restore func())
func SetWriteSeen(f func(what string, off, n int64)) (restore func())
```

App (internal/app/keys.go unless named):

```go
func (c *Core) RemoveSlot(recipientID string, rekey bool, backupPath string) *Error
func (c *Core) ChangeEntangledPassword(rekey bool, backupPath string) *Error
func (c *Core) SetEntangled(on, rekey bool, backupPath string) *Error
func (c *Core) RotateNow(backupPath string) *Error
func (c *Core) BeginEnroll(o EnrollOptions) *Error // EnrollOptions gains WithRecoveryKey bool
func (c *Core) ReplaceRecoveryKeys(backupPath string) *Error // 2.7
func (c *Core) beginAs(kind string, o actOpts, fn func(cer *ceremony, unl *keystore.Unlocked) error) *Error
func (c *Core) landChange(cer *ceremony, unl *keystore.Unlocked, rekeys bool, commit func() error) error // replaces rotateWith
func (c *Core) backUpAfter(cer *ceremony, unl *keystore.Unlocked, path string)
func (c *Core) backupPlaceOK(path string) bool // !reservedName(path) && !samePath(filepath.Dir(path), c.deps.DataDir)
func (c *Core) recordExport(gen uint64, recovery []string)
func (c *Core) finishRetireLocked(unl *keystore.Unlocked) error           // session.go
func (c *Core) publishUnlockedLocked(ks *keystore.Keystore, unl *keystore.Unlocked) error // session.go; now returns
func (c *Core) retireBarrierLocked() *Error                               // session.go, 2.4
func (c *Core) publishRegistryAtLocked(allowForgotten bool, fn func(g *registry, modifiedAt int64) error) *Error // session.go
func (c *Core) publishRegistryLocked(fn func(g *registry) error) *Error  // session.go
func (c *Core) publishRegistry(fn func(g *registry) error) *Error        // session.go
func (c *Core) publishRegistryAt(allowForgotten bool, fn func(g *registry, modifiedAt int64) error) *Error // records.go, beside updateRegistryAt
func afterFactsOf(ks *keystore.Keystore) *AfterFacts                      // keys.go: CeremonyState.After from the handle's plaintext facts (2.5)
func (cer *ceremony) keep(i, n int, url, id string) error                 // 2.7: StepKeepKey and its prompt
func (cer *ceremony) confirm() error                                      // 2.7: StepConfirm and its prompt

// vaultErr: a record of the vault file that does not open (format.ErrInvalid,
// which keystore's corrupt wraps) is the vault's problem, not an archive's,
// as the reveal and the merge already say (keys.go:730-733, merge.go:80-84).
func vaultErr(err error) error // errors.Is(err, format.ErrInvalid) -> coded(CodeVaultInvalid); else err

// recoveryHex is the hex recipient IDs of the active recovery slots, the
// recovery keys a backup made now holds (mutate.go:818-824).
func recoveryHex(slots []keystore.SlotInfo) []string
```

`acquireUnlocked` reads `cer.without` and `cer.recoveryProof` (2.3); the `ceremony` struct gains
both. `seams` (internal/app/archives.go, the struct beside `redeciding` and `mounting`) gains
`rekeyed func()` ("a re-keying act's commits have landed, before the next Session is installed"),
`committed func()` ("an act's change has landed, before its retirement") and `failAt func(point
string) error` ("commit", "retire" or "session": fails the act's keystore write there, the
retirement the act, the unlock or the barrier would make, or the next Session's derivation after a
landed commit, with what it returns, before calling it); nil outside a test, as their siblings. The
vault state gains `generation uint64`, `retireOwed bool` and `retireFailed bool` (2.4), and
`VaultStatus` (types.go:22-80) gains `RetireOwed bool` (`json:"retireOwed"`): the cached
`retireOwed`, for the import's confirmation (2.4). `lastExport` (settings.go:15-18), `BackupStatus`
(types.go:102-107), `FileInfo` (types.go:548-566), `CeremonyState` (types.go:146-173),
`EnrollOptions` (keys.go:152-156) and the cached facts (session.go:180-197) gain the fields of 2.3
and 2.5. internal/app/errors.go gains two codes, beside `CodeVaultStale` (errors.go:33) and
`CodeSetupNeeded` (37):

```go
	// CodeRetireOwed: a change's retirement failed and the vault file still
	// holds its earlier state (FORMAT.md R41). A standing warning; the
	// refusal of a mutation that writes a record or a secret, or a backup,
	// while it stands (the barrier, 2.4); and the act's BackupError when its
	// backup was not taken for that reason. Each of those mutations, and the
	// next unlock, tries to finish it first.
	CodeRetireOwed Code = "vault.retire_owed"
	// CodeSetupPasswordOn: Finish setting up refused before its ceremony on
	// a vault whose password is on, whose first way in AddFirstWayIn cannot
	// write; the vault is unlocked with a recovery key and given a YubiKey
	// on the Vault page instead.
	CodeSetupPasswordOn Code = "vault.setup_password_on"
```

api.go (internal/app/api/api.go:350-377):

```go
// BeginEnroll adds a way in: "token", "password" or "recovery". An
// enrolled key inherits the vault's entangled password and is wrapped from
// the kept K_P, so no password is chosen here (APP.md §13). withRecoveryKey
// proves with a recovery key instead of a YubiKey: the user has none of the
// vault's at hand; for a YubiKey or a recovery key added to a vault with a
// YubiKey and a recovery key (ruled 2026-10-27).
func (k *Keys) BeginEnroll(kind, label string, withRecoveryKey bool) error

// SetEntangled turns the vault's password on or off; turning it on asks
// for the new password in the ceremony, and rekey re-keys the vault in the
// same commit. Turning it off takes neither rekey nor a backup.
func (k *Keys) SetEntangled(on, rekey bool, backupPath string) error

// ChangeEntangledPassword replaces the vault's password; a ceremony, and the
// old password is never a field. rekey re-keys the vault in the same commit;
// a backupPath that is not empty exports a backup inside the ceremony, after
// the change.
func (k *Keys) ChangeEntangledPassword(rekey bool, backupPath string) error

// RemoveSlot removes an unlock method; rekey re-keys the vault in the same
// commit; backupPath as above.
func (k *Keys) RemoveSlot(recipientID string, rekey bool, backupPath string) error

// RotateNow re-keys the vault; backupPath as above.
func (k *Keys) RotateNow(backupPath string) error

// ReplaceRecoveryKeys makes as many new recovery keys as the vault has,
// shows each to be kept, and on the user's confirmation replaces every old
// one and re-keys the vault in one commit; backupPath as above. The digits
// arrive over the one-time URL, never here.
func (k *Keys) ReplaceRecoveryKeys(backupPath string) error
```

Error codes: two new (errors.go:23-65): `vault.retire_owed`, a warning and the barrier's refusal
while it stands (2.4), and `vault.setup_password_on`, *Finish setting up*'s refusal (2.3). Before
any ceremony: `params` (a bad ID; a backup path reserved or in the data folder; turning the password
off with `rekey` or a backup; the switch off, for the change; `WithRecoveryKey` where 2.3 refuses
it), `vault.tampered`, `vault.slot_not_found`, `vault.invariant`, `vault.no_recovery_slot`,
`vault.setup_needed`, `vault.setup_password_on`, `vault.needs_unlock`, `ceremony.in_progress`,
`op.in_progress` (keys.go:192-212, and a Delete's claim, 2.4), and from the barrier
`vault.retire_owed`, `vault.tampered` (a Tampered vault still owing a live copy's own tail, 2.4), or
`vault.indeterminate` and `vault.conflict` entering Broken. Inside: `ceremony.cancelled`; the
proof's token codes and `vault.auth`; `vault.password_short`, the prompt standing
(ceremony.go:215-219); `vault.invalid` for a fail-closed rotation, through `vaultErr` (it was
`archive.invalid`, errors.go:386); `vault.indeterminate` and `vault.conflict`, from the change or
its retirement, entering Broken; the keystore refusals of 2.3 as a backstop. The backup's file
codes, as `ExportBackup` has them, become the act's `BackupError`, never its error, and so does
`vault.retire_owed` when the backup is not taken because the retirement did not land (2.3); the
retirement's other errors become its `RetireError`. At the barrier of a registry mutation (2.4):
`vault.retire_owed`, `vault.tampered`, or `vault.indeterminate` and `vault.conflict` entering
Broken, each before anything is written.

**Keystore tests** (internal/keystore/keystore_test.go; helpers at 19-155, `snapshot`/`restore`
at 690-704, `sbCopy`/`putSB` in seq_test.go:20-39). "The raw bytes hold no X" below means the
file read whole with `os.ReadFile` and searched with `bytes.Contains` for each of X's 16-byte
recipient ID, its `epk` (a public key, FORMAT.md:1010) and its 56-byte `wrapped_vmk`, which
appear in plaintext only in a slot region copy (the registry is ciphertext); all three, since a
torn write can keep one without another (critique 2, F6). "The registry area is clean" means no
4 KiB unit of `[0x42000, the file's length by os.Stat)` holds a nonzero byte outside the two
registry extents the superblock copies name, read from the raw bytes and never from the handle
(critique 3, finding 1). Two new helpers:

```go
// metaOf is the Metadata key an Unlocked of this file derives (its Session's
// meta, package-internal): what a holder of that generation's VMK has.
func metaOf(t *testing.T, u *Unlocked) []byte

// knownPlaintext is the outside review's finding 1 as an attacker: with a
// Metadata key and the plaintext of an earlier registry of the same vault
// (from an earlier copy), it takes every 4 KiB-aligned offset s of the
// registry area as a registry's start and every 16-byte block p at or after
// s as that registry's block (p - s)/16, recovers a counter block by
// AES-decrypting the ciphertext XOR the guessed plaintext, and decrypts the
// file's following bytes in counter mode from it; it returns what it
// decrypted, for a test to search for an archive ID or a wrapped key.
func knownPlaintext(file []byte, meta []byte, earlier []byte) [][]byte
```


1. `TestRemoveSlotAndRotate`: the set TestRemovalThenRotation builds (1767-1781: a hardware slot
   with the vault's password, recovery "paper" and "spare") plus one `archive` record;
   `RemoveSlotAndRotate(spare)` from generation 1: one commit (seq + 1), generation 2, history
   [1], no escrow record for "spare", K_P unchanged, the archive key unwraps under the new KWK,
   and the same Unlocked derives a working Session; reopened, the token with the password and
   "paper" open it, and the spare's key fails the verifier (`ErrVerifier`, as 1830-1834).
2. `TestRemoveSlotAndRotateRefusesBeforeDrawing`: `ErrNotFound` for an unknown ID, `ErrInvariant`
   for a removal TestInvariant's set forbids (383-515); the file's bytes are equal before and
   after (`snapshot`) and the generation stays.
3. `TestRemoveSlotAndRotateCrashBeforeFlip`: as TestCrashBeforeFlip (1139-1175): the flipped
   superblock copy restored to its pre-commit bytes opens at generation 1 with the slot, and the
   removed credential still unlocks it (the old state whole); unrestored, it opens at 2 without.
4. `TestRemoveSlotAndRotateDefeatsTheSplice` (before any `Retire`): point the live superblock at
   the inactive copy's region, setting BOTH `slot_region_off` and `slot_region_len` from the
   inactive copy's own superblock (FORMAT.md:926-927), and recompute its checksum, as
   TestGenerationMismatchIsTampering edits a superblock (1840-1866); unlocking with the removed
   credential is `ErrTampered` and yields no handle (FORMAT §6.2).
5. `TestRemoveSlotAndRotateFailsClosed`: a secrets record that does not open (as
   TestRotationFailsClosed, 1673) refuses with `format.ErrInvalid`; nothing committed; the slot
   still there.
6. `TestRemoveSlotAndRotateOnATamperedRegion`: `ErrTampered`, as TestTamperedRegion's removal
   (803-847).
7. `TestChangeEntangledPasswordAndRotate`: a token and a recovery key, from generation 1:
   generation 2, history [1], a new `entangle_salt`, the entangled_key record opening under the
   new KWK_secrets to the handle's new K_P and not to the old; reopened, the token opens with the
   new password and not the old (`ErrAuth`), and the recovery key opens. A secrets section whose
   entangled_key does not open refuses (`format.ErrInvalid`) with nothing committed.
8. `TestChangeEntangledPasswordAndRotateRefusedWhileOff`: `ErrParams`; the file unchanged.
9. `TestChangeEntangledPasswordAndRotateCrashBeforeFlip`: restored as in 3, the old password
   opens at generation 1.
10. `TestSetEntangledAndRotate`: a token and a recovery key with the switch off, from generation
    1: generation 2, the header's entangle 1 and a new salt, exactly one entangled_key record,
    opening under the new KWK_secrets to the handle's K_P; reopened, the token opens with the
    password and not without it. `ErrParams` while the switch is on; `ErrInvariant` on a set whose
    active slots are all hardware, with the file's bytes unchanged and no Argon2id run (the
    `Entangle` given carries parameters that would fail if derived).
11. `TestReplaceRecoveryAndRotate`: a token with the password on, recovery "safe" and "office",
    one `archive` record, from generation 1; two `PrepareRecovery` with the labels "safe" and
    "office"; `ReplaceRecoveryAndRotate` of both: one commit, generation 2, history [1]; exactly
    two escrow records, for the new IDs, each opening under the new KWK_secrets to its key and
    passing `keyFitsSlot` (mutate.go:550-560); no record of slot_type 3 with an old ID; K_P
    unchanged; the archive key unwraps under the new KWK. Reopened: each new key opens it at 2,
    each old key fails the verifier (`ErrVerifier`), the token with the password opens it; the
    new records' labels are "safe" and "office". The `RecoveryKey` reveal of each new slot
    returns its key.
12. `TestReplaceRecoveryAndRotateRefuses`: `ErrParams` for none prepared; `ErrDuplicate` for one
    prepared twice; `ErrTampered` on a tampered region; each with the file's bytes unchanged.
    `TestPrepareRecoveryNamesTheSlotItMakes`: the prepared record's public key is the one
    `recoveryKeys` derives from the key (slots.go:295-305), its ID's first four bytes differ
    from every active slot's and from every other prepared slot's (a test reader that returns
    a colliding prefix first, as the redraw's test seam, shows the redraw), its key is held in
    a locked page and is zero after `Zero`, and `PrepareRecovery` writes nothing (`snapshot`
    equal).
13. `TestReplaceRecoveryAndRotateCrashBeforeFlip`: restored as in 3, the old keys open at
    generation 1 and the new keys do not (`ErrVerifier` or `ErrNoSlot`): a replacement whose flip
    did not land added nothing.
14. `TestRetireOwedIsReadFromTheBytes`: a new file is not owed (`Create`, and an `Export` of it);
    after `AddSlot` it is; after `Retire` it is not; after a registry-only save of a retired file
    it is not (the losing copy one save behind), including a save that shrinks the registry at
    `0x42000` (its scrub left nothing); each answer is the same from a fresh `Open` of the file; a
    `Retire` with nothing owed writes nothing (`snapshot` equal, seq unchanged). And the handle's
    flag follows a failed commit (critique 2, F5): on a retired file, `commitFault("registry")`
    set for one `AddSlot`, which fails; `RetireOwed()` is true on the same handle at once; a
    `Session.UpdateRegistry` then carries the retirement without a reopen (`RetireOwed()` false,
    the raw bytes hold no record of the slot that was never added, the registry area is clean).
    And a registry-only save that fails at "sync" after writing its registry leaves the handle
    owed by clause 4 alone; a `Retire` then makes no commit (seq unchanged) and scrubs, after
    which nothing is owed and the registry area of the raw bytes is clean (critique 3, finding 1:
    the handle's answer alone would pass while bytes past `k.size` remained). After every flip in
    this test `FileSize()` equals the file's length by `os.Stat`.
15. `TestRetireLeavesNoPreviousState`: for each of `Rotate`, `RemoveSlotAndRotate`,
    `ChangeEntangledPasswordAndRotate`, `SetEntangledAndRotate`, `ReplaceRecoveryAndRotate`,
    `RemoveSlot` and `ChangeEntangledPassword`, followed by `Retire`: the raw bytes hold no
    recipient ID of a removed or replaced slot; close; damage the live superblock copy (one byte
    inside its checksummed range); reopen. The file opens through the other copy at the
    generation the act wrote, and what the act withdrew fails there: a removed or replaced
    recovery key (`ErrVerifier`), the old password with the token (`ErrAuth`), the token without
    the password (the keystore's `ErrPasswordRequired`). Without `Retire`, the raw bytes still
    hold the removed ID and the same damage opens the state before the act, where the withdrawn
    credential opens it: the test asserts that too, so it fails if `Retire` stops retiring.
16. `TestRetireFaults`: after `RemoveSlotAndRotate(spare)`, `commitFault` set for one step at a
    time. "scrub" (the retiring commit landed, its scrub refused): `Retire` returns the error,
    `Broken()` is nil, and `RetireOwed()` is true by clause 4 alone wherever the retiring commit's
    registry did not overwrite the pre-change one (2.2.3's placement table, rows two and three;
    the test reads both superblocks' `registry_off` to know which row it is in, and asserts the
    first row's trim instead); a second `Retire` makes no commit and leaves the registry area
    clean. "region", "registry" and "sync": `Retire` returns the error, `Broken()` is nil,
    `RetireOwed()` is still true; a fresh `Open` agrees, unlocks at generation 2 without the
    spare, and its own `Retire` succeeds, after which the raw bytes hold no spare. After
    "registry" and "sync" Enfold's reader no longer opens the losing superblock's state (damage
    the live copy as in 15: `Open` or `Unlock` fails rather than yielding the state before).
    "superblock" (the bodies written and the barrier passed, the superblock write skipped) and
    "flipsync": `ErrIndeterminate`, the handle Broken. Reopened, generation 2 without the spare
    either way. After "flipsync" (flipped) nothing is owed. After "superblock" the other region
    copy is already the live region followed by zeros, so the regions alone say nothing is owed,
    while the losing superblock copy still decodes at generation 1 naming its old region and
    registry: `RetireOwed()` is true by the predicate's third clause (critique 2, F1); the next
    `Retire` writes that superblock copy (its `seq` is the live one's plus one and its
    `registry_nonce` differs from the one before), after which nothing is owed.
17. `TestARegistrySaveCarriesAnOwedRetirement`: after `RemoveSlot(spare)` with no `Retire`, a
    `Session.UpdateRegistry` that changes one archive record: `RetireOwed()` false after it; the
    raw bytes hold no spare ID; the live superblock's `slot_region_off` names the other copy; the
    registry's `SlotRegionHash` is the one before; reopened and unlocked, not tampered, the record
    changed. With `commitFault("region")` set for that save: the carried pass fails before the
    flip and the save is made again without it (2.2.3, rule 4): one commit lands (`seq` + 1), the
    record changed, the other region copy's bytes as they were, still owed. With
    `commitFault("registry")` set: both attempts fail, nothing either wrote counts (the record
    unchanged after a reopen), still owed.
18. `TestATamperedRegionIsRetiredWithoutCopyingItForward` (critique 3, finding 3 and nit 7): a
    tampered live region (as TestTamperedRegion, 803-847) on a file owed by both its regions and
    by clause 4 (`RemoveSlot(spare)` with no `Retire`, then a registry-only save that fails at
    "sync" after writing its registry): `Retire` returns nil; `writeSeen` shows one registry-only
    commit (a registry and a superblock, no region), one "zero" write covering exactly the slot
    region copy neither superblock copy then names, and the scrub's writes, and none to the live
    region copy; afterwards nothing is owed, both superblock copies name the live region copy, the
    raw bytes hold no spare record, the registry area is clean, and the registry's
    `SlotRegionHash` is the one before (reopened, the unlock is `ErrTampered` still). On a
    tampered image whose live copy also holds a legacy tail past `slot_region_len`: the same
    writes, then `ErrTampered`, owed by clause 1 alone, the live copy's bytes unchanged. A
    `Session.UpdateRegistry` on a tampered region succeeds (R25 lets it), carries no retiring
    pass, leaves the live copy's bytes as they were, and scrubs its own remnant. (Revision 3's
    test 18 pinned the app's gate standing aside on a Tampered vault, and revision 4's refused
    every clause; the app's half is app test 31.)
19. `TestRetireZeroesALegacyTail`: a file whose live copy holds non-zero bytes past
    `slot_region_len` (written into the image, as a writer before this change could leave them):
    owed; one `Retire` makes exactly two commits (`seq` + 2), the first into the other copy and
    the second, once that one is live, into the copy that held the tail; both copies are the live
    region followed by zeros; at no point was the live copy written (`writeSeen` shows two region
    writes, each to the copy not live at its time); not owed (critique 2, F2).
20. `TestRegionWriteZeroesTheRestOfTheCopy`: a region of four slots committed, then one of three
    into the same copy two commits later; the bytes of that copy past the new `slot_region_len`
    are all zero.
21. `TestAnEarlierCopyReachesNothingAfterTheWholeCure` (the outside review's finding 4, end to
    end, with critique 2's F7): a token A with the password on and a recovery key R, one
    `archive` record; the file's bytes saved as an earlier copy; then a token X added by "the
    thief" with the VMK R yields (`AddSlot(X)` on an Unlocked opened with R), then `AddSlot(B)`,
    `RemoveSlotAndRotate(A)`, `ChangeEntangledPasswordAndRotate`, `ReplaceRecoveryAndRotate` of
    R and `RemoveSlotAndRotate(X)`, each followed by `Retire`. The earlier copy, opened with R,
    yields a VMK and a K_P: neither the current registry's archive key wrappers, nor its escrow
    records, nor its entangled_key open under the KWKs that VMK derives; that K_P is not the
    current one; R opens nothing in the current file (`ErrVerifier`); X finds no slot; and the raw
    bytes hold no A, no R and no X. Two halves are asserted to fail without their step: without
    the replacement, the R the copy escrows opens the current file; without X's removal, X with
    the new password opens it.
22. `TestATornRetiringWriteStaysOwed` (critique 2, F6): after `RemoveSlotAndRotate(spare)`, write
    by hand only the first 512 bytes of the live region into the other copy, leaving the rest of
    that copy as it was; close; reopen: owed; the raw bytes still hold the spare's record where it
    lay past the part written; the losing superblock and its registry are intact; one `Retire`
    clears it (the raw bytes hold no spare, nothing owed).
23. `TestALosingSuperblockAtAnOldGenerationIsOwed`: the predicate's third clause on its own: a
    file retired at generation 2 whose losing superblock copy is overwritten by hand with a valid
    copy (`putSB`) at generation 1 naming the other region copy, which holds the live region:
    owed; the same copy at generation 2 naming the live region copy: not owed; a losing copy that
    fails its checksum: owed; each followed by `Retire`, after which nothing is owed and the
    losing copy is the retiring commit's predecessor.
24. `TestSessionRetire`: after `RemoveSlot(spare)` with no `Retire`, a `Session` derived from the
    same Unlocked: `Session.Retire()` lands one retiring commit with the registry's records
    unchanged and only `modified_at` advanced; `RetireOwed()` false; on a tampered region owed by
    its regions it retires as test 18 does, with no region write, and returns nil.
25. `TestARegistryWritePadsItsLastUnit`: a registry written at `0x42000` below the live one: the
    bytes from its tag's end to the next 4 KiB boundary are zero, though the unit held nonzero
    bytes before; a registry written after the live one at `k.end` is not padded past it (the
    file's length is the extent's end); on a hand-made file whose live registry begins off
    the grid inside that unit, the padding stops where the live registry begins and the live
    registry still opens.
26. `TestNoRemnantAfterAShrink` (the outside review's finding 1, shrinking): a vault at
    generation 1 with ten `archive` records; the file's bytes and its registry plaintext kept as
    the attacker's earlier copy, with `metaOf` of an `Unlocked` it yields; then archive X added
    (the registry grows, placed after the live one), then eight records removed in two saves (the
    second shrinks at `0x42000`, leaving the tail of the longer registry, X's record in it, in the
    gap), then `RemoveSlotAndRotate(spare)` and `Retire`. The registry area is clean, and
    `knownPlaintext(file, meta1, earlier)` returns nothing that holds X's archive ID or its
    `wrapped_archive_key`. The control, which shows the attacker works: the same sequence with
    `commitFault("scrub")` refusing every scrub, after which the attacker finds X's wrapped key
    in the gap and unwraps it under the generation-1 KWK to X's archive key.
27. `TestNoRemnantAfterAGrowth`: as 26, but the registry grows past its slack at the change, so
    that the change's registry and then the retiring commit's land after the live one and the
    pre-change registry lies below them unnamed (2.2.3's placement table, third row): after
    `Retire`, clean and nothing found; the control finds X.
28. `TestNoRemnantAfterAFailedWrite` (critique 3, finding 1: one handle, no reopen): at
    generation 1, a save that adds X fails at "sync" after writing its registry (sealed under the
    generation-1 Metadata key, in space no superblock names, past `k.size`): owed at once by
    clause 4, and `k.end` is past `k.size`; then, on the same handle, `RemoveSlotAndRotate(spare)`
    and `Retire`. After the change's flip the file's length by `os.Stat` is the later named
    extent's end and equals `FileSize()` (the trim cut the failed registry's tail); after
    `Retire` the registry area is clean, nothing is owed, and the attacker with `meta1` finds
    nothing of X; the control finds it. Run three ways: the live registry at `0x42000`, so that
    the failed save's registry and the change's both land at the boundary after it; the live
    registry above `0x42000` after a growth, so that the failed save lies past both; and a failed
    save longer than the change's registry (X's record and the spare's escrow in it, neither in
    the change's), so that the change's registry, written at the same target, ends inside the
    failed one. A fourth run fails the trim's truncation (`commitFault("trim")`): the tail past
    the named end is zeroed by the scrub instead, the registry area is clean, and `FileSize()` is
    the file's length.
29. `TestTheScrubNeverWritesANamedUnit`: `writeSeen` records every scrub write across a seeded
    sequence of 60 saves whose registry lengths grow and shrink at random, with a rotation and
    its `Retire` every 10: no scrub write touches a 4 KiB unit that held a byte of either named
    registry extent at that moment, none lies below `0x42000` or past the file's end, and after
    each commit the registry area is clean. Each state along the way also opens through the
    losing superblock copy (the live one damaged as in 15), so the scrub never cost the fallback
    of FORMAT.md:879-882.
30. `TestAnInterruptedScrubStaysOwed`: after a retiring commit, half of an unnamed unit's
    nonzero bytes zeroed by hand (a torn scrub) and the rest left: owed by clause 4; `Open`
    agrees; its `Retire` makes no commit (seq unchanged), scrubs, and nothing is owed; the
    registry area is clean.
31. `TestALegacyRegistrySlackTakesTwoPasses`: a legacy file whose live registry's last 4 KiB unit
    holds, past its tag, the bytes of an older registry's record (written into the image): owed
    with `mixed`; the scrub alone leaves that unit (`writeSeen` shows no write to it while it is
    named); one `Retire` makes two commits and their scrubs, after which the registry area is
    clean and nothing is owed; at no point was a unit of a named registry written.
32. `TestAFailedReplacementIsRetiredAway` (critique 3, finding 5): `ReplaceRecoveryAndRotate` of
    two prepared keys with `commitFault("sync")`: an error, generation 1, owed; the raw bytes hold
    the new recipient IDs (the control); then `Retire`: the raw bytes hold no new recipient ID,
    `epk` or `wrapped_vmk`, the registry area is clean, nothing is owed, the old keys open at
    generation 1 and the new ones find no slot. The same for `AddSlot` of a hardware slot with
    `commitFault("registry")`: no record of it is left after `Retire`.
33. `TestTheScrubChecksTheDiskFirst` (critique 3, nit 12): `commitFault("scrub")` writes by hand
    (`putSB`) a valid superblock copy with a higher `seq`, as a writer that ignores the lock would,
    and returns nil: the scrub writes nothing (`writeSeen`), a `Session.UpdateRegistry` whose
    scrub meets it returns nil (its flip landed) and leaves `Broken()` wrapping `ErrConflict`, and
    the next save is refused with it; `Retire` in the same position returns `ErrConflict`.
34. Unchanged and passing: TestRotate (706), TestHandlesAcrossRotation (939), TestCrashBeforeFlip
    (1139), TestRotationSecrets (1586), TestRotationFailsClosed (1673), TestRemovalThenRotation
    (1767), TestGenerationMismatchIsTampering (1840), TestEntangleSwitch (2176), the seq tests
    (seq_test.go): the refactor keeps `Rotate`, `RemoveSlot`, `ChangeEntangledPassword`,
    `SetEntangled` and the commit order.

**App tests**, in a new internal/app/rekey_test.go so that no test file is reworked beyond its
call sites (helpers in internal/app/fakes_test.go: the recorder's `waitCeremony` at 172, the fake
card layer at 215-575, `newHarness` and `newHarnessEntangled` at 690-745, `unlockWithPassword` at
747). Where a test below sets `commitFault` or watches `writeSeen`, it does so through
`keystore.SetCommitFault` and `keystore.SetWriteSeen` (2.6), restores the previous value with the
function they return, and does not run in parallel:

1. `TestRemoveSlotAndRekeyIsOneCeremony`: `newHarness`, `unlockWithPassword`, a second password
   enrolled as core_test.go:1529-1540 does; `RemoveSlot(second, true, "")`; exactly one
   `StepPassword` prompt; `waitCeremony(StepDone)`; every event of the act has `Kind == "remove"`
   and `Rekey`; one event has Phase "commit"; the slot is gone; the handle's generation is 2; both
   superblock copies of the file decode at generation 2 (`sbCopy`); `RetireOwed()` false; still
   Unlocked, and a registry write through the core succeeds.
2. `TestRemoveSlotWithoutRekeyKeepsTheGeneration`: the same with `rekey` false: generation 1,
   `Rekey` false, both superblock copies without the removed slot, the raw bytes without its ID.
3. `TestRemoveSlotIsRefusedBeforeTheProof`: a slot whose `Removable` is false -> `vault.invariant`
   returned at once and no `vault.ceremony` event recorded; an unknown ID ->
   `vault.slot_not_found`; Tampered first -> `vault.tampered` (a tampered vault made as
   internal/app/entangle_test.go:897-937 makes one).
4. `TestRemovingTheOnlyYubiKeyProvesWithARecoveryKey`: a vault of one hardware slot and two
   recovery keys (made as `newHarnessEntangled` makes one, with a second `RecoverySlot` in its
   specs), unlocked with the token; `RemoveSlot(token, true, "")`: the first prompt is
   `StepRecovery`, Method "recovery", `Without` the token's ID; the digits answered as
   core_test.go:104-110 answers the lock screen's -> Done at generation 2, the token gone. On a
   fresh copy, `RemoveSlot(token, false, "")` waits for a YubiKey, as today.
5. `TestRemoveSlotBacksUpAfterTheChange`: `RemoveSlot(second, true, path)`: one proof; Done; the
   file at `path` opens with the recovery key at generation 2 and holds no password slot;
   `LastExportAt` is set and the stamp's `Gen` is 2 and its `Recovery` the vault's one ID; the
   final state has `BackedUp`; `BackupStatus().VaultRekeyed` is false.
6. `TestABackupThatFailsLeavesTheActDone`: `RemoveSlot(second, true, path)` with a file already at
   `path`: Done, `BackupError` the export's file code, `BackedUp` false, generation 2,
   `LastExportAt` unchanged, `BackupStatus().VaultRekeyed` true (the earlier stamp at generation 1).
7. `TestBackupRefusals`: a reserved name and the data folder -> `params`; a backup when no recovery
   key would remain -> `vault.no_recovery_slot`; `SetEntangled(false, true, "")` and
   `SetEntangled(false, false, path)` -> `params`; each at once, no ceremony.
8. `TestRekeyEndsDoneWhenALockLandsAfterTheCommit`: `seams.rekeyed` calls `c.LockNow(...)`; the
   act, begun with a backup path, ends `StepDone`, not `vault.needs_unlock`; no file at the path;
   the vault is Locked; unlocked again with the remaining password, it is at generation 2 without
   the removed slot.
9. `TestTheRetirementRunsWhenALockLandsBetweenTheCommits`: `seams.committed` calls
   `c.LockNow(...)`; the act ends Done; reopened, the file is not owed and its raw bytes hold no
   removed ID.
10. `TestARetirementThatFailsLeavesTheActDoneAndOwed`: `seams.committed` sets
    `commitFault("region")`, so that every region write fails from the moment the change has
    landed: Done with `RetireError`; `Status().Warnings` holds `vault.retire_owed`; the raw bytes
    still hold the removed slot. With the fault still set: `CreateArchive` is refused with
    `vault.retire_owed` and no archive file is left at its path; the archive's listing and an
    extract of a file in it work; an add of one file to an existing archive is admitted and lands,
    and its receipt lands too, its carried pass failing and the save made again without it
    (2.2.3, rule 4), the file still owed and the warning standing. The fault cleared,
    `CreateArchive` is admitted: the retirement lands first (`RetireOwed()` false before the
    create's registry commit, whose `modified_at` is after the retiring commit's), the warning is
    gone, the raw bytes are clean. A second run clears the fault before the add instead: the add's
    receipt carries the one pass a file this writer made needs, after which nothing is owed and
    the warning is gone without any held mutation having been pressed.
11. `TestUnlockFinishesAnOwedRetirement`: as 10 before the fault is cleared, then `LockNow` (the
    warning stands on the lock screen's status), then the fault cleared and an unlock with the
    password: by the first `vault.state` that says Unlocked, `RetireOwed()` is false, the warning
    is gone, the generation unchanged; the receipts and the purge ran after it (a receipt owed as
    TestRegistryWritesWaitForMutation owes one, mutation_test.go:45-104, is paid at a seq above
    the retiring commit's). With the fault still set at that unlock: the vault is Unlocked, the
    warning stands, an archive's listing works, an add lands with its receipt, `ForgetArchive` is
    refused with `vault.retire_owed`, and a forgotten record past its retention is not purged (it
    is purged at the unlock after the fault clears).
12. `TestARetirementThatCannotBeConfirmedIsBroken`: `seams.failAt("retire")` returns
    `keystore.ErrIndeterminate`: the act ends Failed with `vault.indeterminate`, the state Broken;
    Reopen and unlock: generation 2, the slot gone, not owed. The same error from an owed file's
    unlock (`finishRetireLocked`) ends the unlock Failed and the state Broken.
13. `TestChangeEntangledPasswordAndRekey`: `entangledHarness` (entangle_test.go:19);
    `ChangeEntangledPassword(true, "")`; kind "password", `Rekey`; one choose prompt; Done;
    generation 2; after a lock the token opens with the new password and not the old.
14. `TestChangeEntangledPasswordWithoutRekey`: `(false, "")`: kind "password", generation 1.
15. `TestTurnOnAndRekey`: a token harness with the switch off; `SetEntangled(true, true, "")`:
    kind "entangle", `Rekey`; one choose prompt; Done; generation 2; after a lock the token asks
    for the password. `SetEntangled(true, false, "")` on a fresh copy: generation 1.
16. `TestRotateNowBacksUpAfter`: `RotateNow(path)`: one proof; the vault at 2; the backup at 2;
    `BackedUp`. `RotateNow(path)` again, the file now there: Done at generation 3 with
    `BackupError`: the re-key stands, no backup.
17. `TestVaultErrMapsAFileThatDoesNotOpen`: `vaultErr` of a wrapped `format.ErrInvalid` classifies
    as `vault.invalid`; any other error passes through unchanged.
18. `TestInspectFileSaysReKeyedSince`: a backup exported at generation 1, then `RotateNow("")`:
    `InspectFile(backup).Rekeyed` is true and `BackupStatus().VaultRekeyed` true; a new export:
    false for both.
19. `TestReplaceRecoveryKeysIsOneCeremony`: `newHarness` (a password and a recovery key),
    `unlockWithPassword`; `ReplaceRecoveryKeys("")`: one `StepPassword` prompt; then
    `StepKeepKey` with `KeepIndex` 1, `KeepCount` 1, `OldIDs` the old key's ID, `RecoveryID` a
    new ID not equal to it, a URL in `SlotLabel` and a `PromptID`; the digits read through the
    preview's `secretValue` of the URL's token (preview.go:174-185); `SubmitSecret("kept", id,
    "")`; then `StepConfirm` with a `PromptID`; nothing written so far (`snapshot` equal to the
    one taken after the unlock); `SubmitSecret("confirm", id, "")`; Done with `Rekey`, one Phase
    "commit" event, `NewIDs` and `OldIDs` in the final state; generation 2; not owed; the slots
    are the password and one recovery key with the new ID and the old label; after a lock, the
    old digits fail (`vault.auth`) and the new ones unlock; the raw bytes hold no old recovery ID.
20. `TestReplaceRecoveryKeysKeepsTheCountAndTheLabels`: a password, "safe" and "office": two keep
    steps, `KeepIndex` 1 and 2 of 2, in the order the slots are listed, each new slot carrying
    the label of the old one at the same index; the second `StepKeepKey` comes only after the
    first `kept`.
21. `TestReplaceRecoveryKeysCancelledChangesNothing`: `CancelUnlock` at the first keep step, at
    the second and at the confirm step, each on a fresh harness: Failed with
    `ceremony.cancelled`; the file's bytes equal those after the unlock; generation 1; the old
    keys unlock; every handle the act minted is gone (`secretValue` false), dropped by the act's
    own deferred cleanup with no `DropRecoveryKey` from the page (critique 2, F11). A lock
    trigger at a keep step does the same. After a Done, too, no handle the act minted is left.
22. `TestReplaceRecoveryKeysFailedCommitChangesNothing`: `seams.failAt("commit")` returns an I/O
    error after the confirm: Failed with that code; the file's bytes unchanged; generation 1;
    `NewIDs` in the failed state, which the page names (3.3). Then, on a fresh harness, a failure
    at a real write (critique 3, finding 5): `keystore.SetCommitFault` fails the replacement's
    commit at "sync", after its region and registry are written: Failed with the I/O code,
    `NewIDs` in the failed state; the act's own `Retire` ran at once (`RetireOwed()` false when the
    act has ended, no warning); the raw bytes hold no new recovery ID, `epk` or `wrapped_vmk`, and
    the registry area is clean; generation 1; the old key unlocks.
23. `TestReplaceRecoveryKeysRefusedBeforeTheProof`: a vault whose methods are all recovery keys
    -> `vault.setup_needed`; a vault with no recovery key (two YubiKeys, the password off, from
    the fake card layer) -> `vault.no_recovery_slot`; Tampered -> `vault.tampered`; a backup path
    in the data folder -> `params`; each with no `vault.ceremony` event.
24. `TestReplaceRecoveryKeysBacksUpAfter`: `ReplaceRecoveryKeys(path)`: the backup opens with the
    new key at generation 2 and not with the old; `BackedUp`; the stamp's `Recovery` is the new
    ID; `BackupStatus().KeysReplaced` false. On a fresh harness with an export at generation 1
    first, `ReplaceRecoveryKeys("")`: `KeysReplaced` true and `VaultRekeyed` true.
25. `TestEnrollWithARecoveryKeyProof`: a vault of a hardware slot (`fakeCard` A) and a recovery
    key, unlocked with the recovery key; `BeginEnroll(EnrollOptions{Kind: EnrollToken, Label:
    "new", WithRecoveryKey: true})`: the first prompt is `StepRecovery`, Method "recovery"; the
    digits answered; no `StepSwapKey` with a remove label; card B inserted, its PIN, its touch;
    Done; three slots. On a fresh harness, `BeginEnroll(EnrollOptions{Kind: EnrollRecovery,
    Label: "spare", WithRecoveryKey: true})`: the proof is `StepRecovery`, the new key is shown at
    `StepRecovery` after its commit, Done, three slots (critique 3, M6). Refused at once with
    `params`: `WithRecoveryKey` for kind password, and on a vault with no hardware slot.
26. `TestTheLostYubiKeyScenario`, the acceptance scenario of 3.4 in the core, in the order the
    page's chain offers it: a vault of YubiKey A and recovery key R, the password on
    (`newHarnessEntangled`); locked; unlocked with R (`BeginUnlock(MethodRecovery)`);
    `BeginEnroll(token B, WithRecoveryKey)` proved with R, B enrolled; `RemoveSlot(A, true, "")`:
    the wait's state has `Without` A's ID, and only card B answers it (A is never inserted), with
    B's PIN and the vault's password; Done at generation 2; `ChangeEntangledPassword(true, "")`
    proved with B and the old password, the new one chosen: Done at generation 3;
    `ReplaceRecoveryKeys("")` proved with B and the new password: keep, confirm, Done at
    generation 4; locked. Then: A finds no slot; R fails (`vault.auth`); B with the old password
    fails and with the new one opens; the new recovery key opens; the raw bytes hold no A and no
    R; not owed.
27. `TestRemovingAPasswordProvesWithThePassword` (critique 2, F8): a vault of one standalone
    password and two recovery keys; `RemoveSlot(password, true, "")`: the first prompt is
    `StepPassword`, `Without` empty; the password being removed, typed, proves it; Done at
    generation 2 with the two recovery keys. No `StepRecovery` prompt is ever asked.
28. `TestFinishSetupIsRefusedWhileThePasswordIsOn` (critique 2, UX 3): a vault of YubiKey A and two
    recovery keys with the password on; `RemoveSlot(A, true, "")` proved with a recovery key;
    locked: `Status().SetupNeeded` and `Entangled` are both true; `FinishSetup(token)` returns
    `vault.setup_password_on` at once, with no `vault.ceremony` event and the file's bytes
    unchanged; an unlock with a recovery key, then `BeginEnroll(token B)` (proved with a recovery
    key by the existing branch, no switch), Done; `SetupNeeded` false. On a vault whose password
    was turned off before the removal, `FinishSetup` begins its ceremony as today.
29. `TestARegistryMutationWhileOwedFinishesFirst` (R-A; the outside review's finding 3): an owed
    retirement whose every retiring write fails (`commitFault("region")` set once the change has
    landed, as in 10). Refused at once, nothing written (`snapshot` equal after each):
    `CreateArchive` (no file left at its path), `RotateKey` (no operation started), `MergeRecords`
    of an inspected backup, `ForgetArchive` (the archive still open, its running add not cancelled),
    `RestoreArchive`, `DeleteArchive(id, true)` (the archive's file still there), `ExportBackup` (no
    file at the path), `InspectRecords` of a backup (refused at its begin: no `vault.ceremony`
    event, so no proof is asked; critique 3, M8), and `RemoveSlot`, `RotateNow`,
    `ChangeEntangledPassword`, `SetEntangled`, `BeginEnroll` and `ReplaceRecoveryKeys` (each
    `vault.retire_owed`, no `vault.ceremony` event). Admitted and landing: `AddFiles`,
    `ReplaceFile`, `CreateFolder`, `DeleteRecords`, `RenameRecord` and `MoveRecords` in an existing
    archive; `RenameArchive`, `SetArchiveDescription`, `HideArchive`, `Locate` and the vault's lock
    times (the table of 2.4), each registry write among them landing without its carried pass. The
    fault cleared, each refused act is admitted in turn, the first of them after the retirement
    lands and the rest with nothing owed.
30. `TestALegacyTailIsRetiredInFullBeforeAPublish` (the outside review's finding 3, its test): a
    legacy file — the live region copy holding, past `slot_region_len`, the whole record of a
    YubiKey removed without a re-key by a writer before this change (so it wraps the current VMK),
    and the live registry's last unit holding an older registry's bytes past its tag — imported
    and unlocked with the seam failing the unlock's retirement before its first pass; then
    `CreateArchive` with the seam still failing: refused, and the raw bytes still hold the record
    and no new archive record exists (the registry decrypted with the test's Metadata key has no
    such ID). The seam cleared, `CreateArchive` again: `writeSeen` and the file's `seq` show two
    retiring commits and their scrubs before the create's commit; the raw bytes are read after
    every commit (`writeSeen`'s superblock writes mark each flip) and hold the removed record
    after the first retiring commit and not after the second; the new archive's wrapped key is
    first in the file in the create's commit, after which nothing is owed. No retiring pass is
    counted as the barrier's success while `RetireOwed()` is true.
31. `TestATamperedVaultThatOwesStaysReadable` (R-C, finding 4; critique 3, findings 3 and N8): a
    vault owed by its regions (`RemoveSlot` with the seam failing the retirement, so that
    `retireFailed` and the warning stand), then its live region tampered (as
    entangle_test.go:897-937 tampers one), the vault locked, the seam cleared and the vault
    unlocked: the unlock's retirement takes `Retire`'s Tampered branch and lands (no region
    write, the other region copy zeroed, nothing owed), and the vault opens Unlocked and Tampered
    with no `vault.retire_owed` warning; `CreateArchive` lands, as on any Tampered vault;
    `RemoveSlot` is refused with `vault.tampered` as today. A second run, on a legacy image whose
    live copy holds a tail past `slot_region_len`, tampered, with an earlier failed attempt
    having set `retireFailed`: the unlock retires all but that tail and the vault opens Unlocked,
    Tampered and owed by clause 1, with no `vault.retire_owed` warning (N8); the listing and an
    extract work; an add lands and its receipt lands with no retiring pass (the live copy's bytes
    unchanged); `CreateArchive`, `ForgetArchive`, `RotateKey`, `InspectRecords` and
    `ExportBackup` are refused with `vault.tampered`, nothing written, and `CreateArchive` is
    still refused after the add's receipt landed (the verdict is stable); after an import of a
    good copy of the vault (`OpenVaultFile` of a backup adopted through the import), the vault
    opens untampered and the barrier admits. On a Tampered vault that owes nothing,
    `CreateArchive` lands as today.
32. `TestLockAfterAFailedRetirement` (R-C, finding 5): as 10 with the fault set, then `LockNow`:
    `Status()` while Locked carries the warning and `RetireOwed` (which the import's confirmation
    reads, 2.4), and the core's cached `retireOwed` is true with no handle open; an add to an
    archive left open across the lock lands and owes its receipt; `CreateArchive` while Locked is
    `vault.needs_unlock` (no nil handle is read); unlocked with the fault still set, `CreateArchive`
    is `vault.retire_owed`; the fault cleared, it lands after the retirement.
33. `TestAFailedRegionWriteIsRetiredAtOnce` (R-C, finding 5; critique 3, finding 5): an `AddSlot`
    through `BeginEnroll` with `keystore.SetCommitFault` failing once at "registry", after its
    region copy is written: the act ends Failed, "Nothing was changed."; its own `Retire` ran at
    once inside the act (one retiring commit, `RetireOwed()` false, no warning); the raw bytes
    hold no record of the slot never added. With the fault failing every region write instead:
    the act ends Failed, its at-once `Retire` fails too, the cached `retireOwed` is true and the
    warning stands at once; `CreateArchive` is refused with `vault.retire_owed`; the fault
    cleared, `CreateArchive` first retires (its barrier, `seq` + 1 before the create's commit); on
    a fresh harness the same failure followed by `LockNow` and an unlock: the unlock retires
    before the Session (the raw bytes hold no record of the slot never added).
34. `TestTheBackupWaitsForACompleteRetirement` (R-C, finding 8): `RemoveSlot(second, true, path)`
    with the seam failing the retirement: Done with `RetireError` and `BackupError` =
    `vault.retire_owed`; no file at `path`; `LastExportAt` and the stamp unchanged; `BackedUp`
    false. `ExportBackup(path)` afterwards with the seam still failing: refused with
    `vault.retire_owed`; cleared: the retirement lands, then the export.
35. `TestADeleteHoldsSlotChangesWhileItRemovesTheFile`: `DeleteArchive(id, true)` with
    `deleteArchiveFile` held open by a test seam; `RemoveSlot` meanwhile is refused with
    `op.in_progress`; released, the delete forgets the record and the removal can begin.
36. `TestTheCommittedFactsRideTheEnd` (R-C, finding 7): `RemoveSlot(recovery, true, "")` of the
    last recovery key of a vault of two YubiKeys with the password off: the final state's `After`
    has no recovery IDs, `entangled` false and `setupNeeded` false; a replacement's final `After`
    lists the new IDs; an enrolment's lists the vault's.
37. `TestAReplacementWhoseRetirementConflicts` (R-C, finding 9): `ReplaceRecoveryKeys("")` with
    `seams.failAt("retire")` returning `keystore.ErrConflict` after the replacement's commit: the
    act ends Failed with `vault.conflict` and the state Broken; the final state carries `NewIDs`;
    Reopen and unlock with the new key: the Vault page's slots list the new IDs (the replacement
    landed), and the old key fails. The same with `failAt("commit")` returning
    `keystore.ErrConflict`: Failed, Broken, and after Reopen the old IDs are listed (nothing
    landed). The page's classification of both is app-independent (3.5, 3.9).
38. `TestTheBarrierLeavesACeremonysHandleAlone` (critique 3, finding 4; run with `-race`, which
    part B's gate adds for this test, 5): `seams.committed` blocks a removal's ceremony between
    its change and its retirement while the test calls `CreateArchive` from its own goroutine:
    refused with `ceremony.in_progress` at its barrier, whatever the cached fact says (as built,
    the barrier refuses while a ceremony holds the handle: the outside review of the diff,
    finding 5), no archive file left at its path and the vault file unchanged. As built, an
    archive re-key too, refused before its forgotten check and its open (the review of those
    repairs, finding A): a forgotten record's `RotateKey` answers `ceremony.in_progress`, not
    `archive.forgotten`, and a closed archive's opens nothing. The race detector reports
    nothing; released, the removal ends Done.
39. `TestAFailureAfterTheCommitEndsDone` (critique 3, UX M4 and format nit 9):
    `seams.failAt("session")` returns an error after a replacement's commit and retirement have
    landed: the act ends Done, not Failed, with `NewIDs`, `OldIDs` and `After` in its final state;
    the vault is Locked (`ReasonStale`) once the ceremony has ended; unlocked with the new key,
    the slots list the new ID, and the old key fails. The same for `RemoveSlot(second, true, "")`:
    Done, the slot gone, Locked.
40. `TestALostYubiKeyWithNoSpareIsWithdrawn` (critique 3, M6): a vault of YubiKey A and recovery
    key R, the password on; locked; unlocked with R; `BeginEnroll(recovery "spare",
    WithRecoveryKey)` proved with R: the new key shown, Done; `RemoveSlot(A, true, "")`: the first
    prompt is `StepRecovery`, `Without` A's ID; Done at generation 2; `Status().SetupNeeded` true;
    A finds no slot; both recovery keys unlock; the final state's `After` has `setupNeeded` true,
    from which the end offers *Add an unlock method…* (3.6).

Existing call sites to update (signatures and the kind): internal/app/core_test.go:1555
`RemoveSlot(second, false, "")`; entangle_test.go:551, 572, 934 `SetEntangled(true, false, "")`;
entangle_test.go:673, 909 and internal/app/mutation_test.go:497 `SetEntangled(false, false,
"")`; entangle_test.go:619, 705, 758, 912, 937 `ChangeEntangledPassword(false, "")`;
entangle_test.go:633 `s.Kind == "password"` (the change's choose prompts, counted; 683 stays
"entangle", the turn-off's); entangle_test.go:990 and internal/app/merge_test.go:242, 274
`RotateNow("")`; api_test.go:78, 79, 83, 85, 87 `"BeginEnroll(string, string, bool) error"`,
`"ChangeEntangledPassword(bool, string) error"`, `"RemoveSlot(string, bool, string) error"`,
`"RotateNow(string) error"`, `"SetEntangled(bool, bool, string) error"`, and a new line
`"ReplaceRecoveryKeys(string) error"`. The app tests that call `c.BeginEnroll(EnrollOptions{…})`
need nothing: the new field defaults to false. The registry writes the barrier holds switch from
`updateRegistry*` to `publishRegistry*` (ops.go:2104, 2223; records.go:206, 236, 367;
merge.go:434), with the barrier at the early checks 2.4 names (ops.go:2071-2077, 2192-2197;
records.go:200, 301-325); `beginMutation` and `beginWithVMK` (keys.go:179-233) become calls of
`beginAs`, and `InspectRecords` (merge.go:55) calls `beginAs` without `reads`;
`purgeForgottenLocked` (session.go:871) gains its skip. The existing tests of those
calls (archives_test.go, records_test.go, merge_test.go, mutation_test.go) run on files that owe
nothing, and pass unchanged.

### 2.7 (g) *Replace recovery keys…* in the core (ruling 2)

`ReplaceRecoveryKeys(backupPath string) *Error`, kind **"replace"**:

1. **Pre-checks** (2.3), under `c.mu`, while Unlocked: `vault.tampered`, `vault.setup_needed`,
   `vault.no_recovery_slot`, the backup's place. Then `beginAs("replace", actOpts{rekey: true},
   fn)`; `beginAs` refuses `ceremony.in_progress` and `op.in_progress` as every act
   (keys.go:201-212).
2. **The proof**: `acquireUnlocked` as for every act: a YubiKey where the vault has one, else its
   password (keys.go:285, 325). Never a recovery key: step 1 refused a vault without a protector.
   `cer.releaseCard()` at once: nothing after the proof needs the YubiKey (2.2, step 6 of
   `rotate` wraps from public keys).
3. **The old keys**: `olds` = the active recovery slots of `unl.Keystore().Slots()`, in the order
   the page lists them (`Slots`, file.go:215-223, which the Vault page shows in that order,
   KeysPage.svelte:402); `n = len(olds)`; `cer.set` `OldIDs` = their IDs (`recoveryID`,
   core.go:336-339) and `KeepCount = n`.
4. **For each i in 1..n** — making the key, showing it and waiting for it to be kept, one at a
   time, so the second key is not drawn until the first is kept:
   - `rk, err := kdf.NewRecoveryKey()`; `prep, err := unl.PrepareRecovery(rk, olds[i-1].Label,
     preps)`, appended to `preps`: the new slot takes the old one's label, which is where the user
     keeps that sheet ("Recovery key — printed, in the safe"), so the new sheet goes where the old
     one was, and its ID differs from every listed one and every new one in its first four bytes;
   - `id := recoveryID(prep.RecipientID())`; `url, err := cer.mint(rk.Digits(),
     olds[i-1].Label, id)` (ceremony.go:1359-1367: refused once a lock has latched the ceremony),
     the URL's token kept in `minted`;
   - `cer.keep(i, n, url, id)`: sets Step `StepKeepKey`, `SlotLabel` url, `RecoveryID` id,
     `KeepIndex` i, appends id to `NewIDs`, and asks a prompt of kind `kept` (`cer.ask("kept",
     StepKeepKey, PINStatus{})`), which the page answers once the key is kept (3.3). The prompt's
     deadline is every prompt's, the session's absolute default (ceremony.go:107-109, 376;
     settings.go:124). `kdf.Zero(rk[:])` once the prompt is answered or the act ends; the
     prepared slot holds its own copy for the escrow until step 6.
   A cancel, a lock trigger or the prompt's deadline at any of these ends the act through
   `ErrTokenCancelled` with nothing written: no key drawn so far has a slot. The fn's deferred
   cleanup drops every token in `minted` (`c.preview.dropSecret`, preview.go:185-190), whatever
   the outcome: `finish` drops no secret (ceremony.go:290-331), and the keep mode's *Cancel* and
   Escape call only `Vault.CancelUnlock`, so without it a cancelled key's digits would stay
   behind their token for up to ten minutes (`secretLife`, preview.go:157-159) — keys that open
   nothing, but held for nothing (critique 2, F11). A lock still drops them all first
   (`dropAllSecrets`, preview.go:192-197). Dropping a handle removes the core's entry and so
   makes the digits unreachable; it does not erase them. They are an immutable Go string in the
   preview server's map (`mintSecret`, preview.go:164-170) and a JavaScript string in the page
   (`digits`, RecoveryReveal.svelte:53), whose memory the collectors reuse in their own time, as
   for every showing today (the outside review's second reading, its Q2 position; section 6,
   follow-up 9). The prepared slots' copies, in locked pages, are zeroed (`PreparedRecovery.Zero`).
5. **The confirmation**: `cer.confirm()` sets Step `StepConfirm` and asks a prompt of kind
   `confirm`; the page draws each new key beside the one it replaces, by label, and the press
   that answers it is the user's last word (3.3). It is the replacement's one confirmation (ruled
   2026-10-27, R-B): no key's showing asks a second time, whichever way it was kept, as no showing
   has since 2026-09-09 (DECISIONS.md:3326-3334), the replacement's included. Before the commit a
   new key cannot be shown again, and from the commit on the old keys no longer open the vault, so
   the act asks once, for all its keys together, here, where each new key's ID stands beside the
   one it replaces and the place its sheet goes. Revision 3 also asked a second confirmation of
   *I have written it down* in each key's showing, reading ruling 2's quoted words; R-B retired it.
6. **The one transaction**: `cer.check()`, then `landChange(cer, unl, true, func() error { return
   unl.ReplaceRecoveryAndRotate(preps) })` (2.4): Phase "commit"; one commit that removes every
   old recovery record, adds the new ones, seals their escrow under the NEW `KWK_secrets` and
   wraps the new VMK into every slot at the new generation (2.2.1); the retiring commit; the next
   Session. Every `prep.Zero()` is deferred from step 4.
7. **The backup after**: `backUpAfter(cer, unl, backupPath)`: at the new generation, holding the
   new recovery keys only (mutate.go:818-824), the stamp's `Recovery` their IDs; skipped, with
   `BackupError` `vault.retire_owed`, while the retirement is owed (2.3).
8. **The end**: `finish` writes Done (ceremony.go:320-325) with `Rekey`, `OldIDs`, `NewIDs`,
   `After`, `BackedUp` or `BackupError`, and `RetireError` if any; the facts are cached, so the
   page's cards show the new IDs once it reads the slots again at the Done (3.7).

Why the keys are shown before the commit, when the enrolment shows its key after (keys.go:379-401):
an enrolment adds a way in, and a key shown after its commit can be shown again from a protector
if the dialog is lost; a replacement takes ways in away, so the user must hold the new keys before
the old ones stop working, and a key the user never kept must never be the vault's. The order
makes a cancel cost nothing but the sheets already printed, which the page names (3.3).

Why the same number: each recovery key the user keeps is a place it is kept (the labels say where:
"printed, in the safe"), and a replacement that changed the count would change that arrangement
behind the user's back; one more or one fewer is *Add an unlock method…* or *Remove…* afterwards,
each its own act. It also keeps the invariant by construction (2.2.1), the two-key minimum of a
vault whose only methods are recovery keys included.

What a failure after the showing does, in the one classification the page uses (`replaceResult`,
3.5; critique 3, N2). A cancel, a lock trigger, the prompt's deadline, or a failure before the
commit's flip with an ordinary code changes nothing: the act ends Failed, "Nothing was changed.",
the page says the keys shown were never added, by their IDs (3.3), and the act's own `Retire` has
already cleared whatever the failed commit wrote of them (2.4, `landChange` step 3), so that no
sheet the page says to destroy still opens anything with this file. `vault.indeterminate` and
`vault.conflict` are *unknown*, wherever they arise: the first comes from a superblock write whose
outcome the handle cannot know; the second from the replacement's own `checkOnDisk`, before
anything landed, or from the retiring commit after the replacement committed (2.4 step 5). Both
are Broken, whose Reopen shows either the old IDs (nothing happened) or the new ones (it did),
the page saying meanwhile to keep every sheet and never to destroy one (3.3; the outside review's
second reading, finding 9). Once the commit has landed the act ends Done: a retirement or a
backup that fails is said under the title, and a Session that cannot be derived locks the vault
after the Done (2.4, step 7).

---

## 3. The UI change

### 3.1 (a) The remove dialog

It becomes a form (`onsubmit` on Dialog, frontend/src/components/Dialog.svelte:41-49): Enter
presses its submit, including Enter on a radio or the tick (APP.md:3193-3195), and Esc is
*Cancel*. **It opens with the focus on its checked answer**: Dialog's focus effect
(Dialog.svelte:81-93), on a form with no field to type in, first looks for
`fieldset input[type="radio"]:checked` and focuses it, and only then the submit. A screen reader
then reads the question (the legend) and the chosen answer before anything else, which is the
point of DESIGN §5's question, and Enter on it still presses the submit. The selector is scoped to
a fieldset so that no other dialog changes: the extract dialog has a field first
(ExtractDialog.svelte:95-101), and *Finish setting up*'s cards are radios outside any fieldset
(FirstWayIn.svelte:193, 220), so it keeps opening on *Continue* as ruled (APP.md:3196-3200). Its
height does not change with the answer or the tick (only the primary's words do), so it stays
centred, not anchored (APP.md:3201-3208).

Layout, top to bottom, all in the dialog's body (the template, strings.ts:44-49):

1. **Title**, the question that names the object: `Remove ${label}?` (strings.ts:1544), and
   `Remove ${label} (added ${date})?` when another method on the page has the same label: the Add
   dialog names every password "Password" (KeysPage.svelte:593), so two cards can read alike, and
   the card's own line says "added <date>" (KeysPage.svelte:98-102).
2. **Body**, two sentences, by type (a backup is a copy by the glossary, strings.ts:15-16):
   - YubiKey or password: "{label} stops unlocking this vault now. An earlier copy of the vault
     file still opens with it; a backup never held it."
   - Recovery key: "{label} stops unlocking this vault now. Copies and backups of the vault made
     earlier still open with it." (the present words, strings.ts:1545)
3. **Enfold's own copies**, a line in the second ink, only while `VaultStatus.RetiredCopies` is
   above 0 (types.go:76; the page reads it at KeysPage.svelte:496): "Enfold keeps 2 replaced vault
   files in its folder; those that had {label} still open with it until you delete them." (for
   one: "Enfold keeps 1 replaced vault file in its folder; if it had {label}, it still opens with
   it until you delete it."). Replaced copies keep their unlock methods (APP.md:182-185); the
   damaged copy is not counted, since it does not open (APP.md:157-160).
   3b. **The last protector**, a line in the second ink, only when the method removed is a
   YubiKey and no other YubiKey or password would remain (the vault would be left with recovery
   keys alone, which the invariant allows with two of them, slots.go:189-196): "After this the
   vault has only its recovery keys: add the new YubiKey here now, or after unlocking with a
   recovery key." Why: such a vault is *setup needed* (vaultfile.go:200-210). Its lock screen
   still offers *Unlock with recovery key* (Lock.svelte:735-737), so the user is not shut out,
   and the idle lock may land before the user acts, so the line promises nothing about locking.
   What it steers away from is the lock screen's *Finish setting up…*, which this change refuses
   while the password is on (2.3) and which would otherwise run its whole ceremony before
   failing; *Add an unlock method…* on this page works either way (it proves with a recovery key
   there by the existing branch, keys.go:268-283), and the end of this act points to it (3.6).
4. **The question**, a fieldset of two radios with its legend as the field label, marked
   `data-question` (the precedent is ExtractDialog's `.choices`, ExtractDialog.svelte:78-90,
   131-140). The re-keying answer stands first and pre-selected: the dialog opens on it and the
   primary names its act, and a user who reads one line reads the one that re-keys. Each answer
   has a second line in the second ink that says what it does (APP.md:3234-3236), always drawn:
   "The vault is re-keyed, as recommended." and "The vault keeps its key."

   | Method | Legend | First, pre-selected (re-keys) | Second (removes only) |
   | --- | --- | --- | --- |
   | YubiKey (`hardware`) | Why are you removing this YubiKey? | Lost, stolen, or I am not sure | I still have it and it was never out of my control |
   | Recovery key | Why are you removing this recovery key? | Lost, stolen, or I am not sure | I still have it and it was never out of my control |
   | Password | Why are you removing this password? | Someone else may know it, or I am not sure | Only I have ever known it |

   The YubiKey's and the recovery key's answers are DESIGN.md:403-404 word for word; the question
   names the method because "this key" would be a bare key on screen (strings.test.ts:175-183) and
   wrong for the other two. A password has its own words (DESIGN.md:406-407): it is not lost or
   stolen but known.
5. **The limit and what is left to do**, an info line under the question in the second ink with
   its icon, always shown, whichever the answer (ExtractDialog.svelte:118-122, 143-146). Its first
   sentence is DESIGN.md:418-421 said plainly; the rest is FORMAT R38 (FORMAT.md:723-737) and DESIGN
   trap 11 (DESIGN.md:759-761) said as what the user does about it, pointing to the act that does
   it (ruling 2), with the password named where it is on (the outside review's finding 4):
   - YubiKey or password, the vault's password off: "Re-keying protects the future, not the past:
     whatever someone could open until now, they may already have copied. If an earlier copy of the
     vault file may be out there, whoever opened it with this {YubiKey | password} could also read
     the recovery keys, which still open this vault: replace them afterwards with Replace recovery
     keys… on this page."
   - YubiKey, the vault's password on: the same first sentence, then "If an earlier copy of the
     vault file may be out there, whoever opened it with this YubiKey could also read the recovery
     keys and the vault's own record of the password: change the password afterwards, then replace
     the recovery keys with Replace recovery keys… on this page."
   - Recovery key: the same first sentence, then "Whoever has this recovery key can open an
     earlier copy or backup of the vault and read the other recovery keys there: if one may be out
     there, use Replace recovery keys… on this page instead, which removes this one too. Check the
     unlock methods as well, and remove any you did not add." With the vault's password on, " Then
     change the password." follows.
   The second sentence speaks of recovery keys, so it is drawn only while the vault has a
   recovery key besides any being removed (critique 2, UX 12; a vault of two YubiKeys with the
   password off may have none, section 6, follow-up 4); with none, a YubiKey's line ends at the
   first sentence, or, with the password on, says "If an earlier copy of the vault file may be out
   there, whoever opened it with this YubiKey could also read the vault's own record of the
   password: change the password afterwards." The order, the password first, is the order the
   ends' buttons offer (3.6). *Replace recovery keys…* is named, not linked: the dialog's one act
   is the removal, and the end carries the next command as a button (3.6), once the removal it
   follows has landed.
6. **The backup**, the block every dialog that re-keys shares (one rule; 3.5):
   - the last backup as the page's one shape for a fact, a label · value list (APP.md:3227-3228):
     *Last backup* · "22 days ago" or "None recorded here" (the Re-key dialog's own,
     KeysPage.svelte:620; strings.ts:1536-1537);
   - a reason under it, by what ticks the backup: on the re-keying answer, "A backup made before
     the re-key is at the vault's old key, and restoring it would bring that key back; one made
     afterwards is not."; at a recovery key's removal, on either answer, "Backups made while {label}
     was an unlock method hold it and open with it; one made afterwards does not." (the outside
     review's finding 7: a backup made before the key was added never held it,
     mutate.go:818-824); otherwise, while a backup is owed, the Re-key dialog's reason, "A backup is
     what brings your archives back if this vault is lost." (strings.ts:1539);
   - the tick, **Export a backup afterwards**, drawn whenever a recovery key remains after the
     act, and ticked on the re-keying answer, ALWAYS at a recovery key's removal (the backups made
     while it was enrolled hold it), and on the other answer while a backup is owed (none recorded
     here, the last over a week old, made before the vault was last re-keyed,
     `BackupStatus.VaultRekeyed`, or holding only replaced keys, `BackupStatus.KeysReplaced`); the
     user may clear it. Why the re-keying answer ticks it whatever the date (critique 2, UX 1):
     the act's own re-key makes every earlier backup *behind* by the card's rule (`VaultRekeyed`,
     2.3, 3.7), so a dialog that said no backup was needed would be followed at once by a card
     that says one is;
   - where no recovery key would remain (the last one removed from a vault of two YubiKeys with
     the password off, which the invariant allows, slots.go:155-197), the line "No backup of this
     vault can be made without a recovery key." in the tick's place.
   It asks and never refuses, as APP.md:3812-3813 says of the Re-key dialog; `lastExportAt` still
   gates nothing (APP.md:3825-3827). The tick follows the answer: it is set by the rule when the
   dialog opens and again whenever the answer changes, and a clear the user made stands until
   then. Only its state and the reason's words change, so the dialog's height does not.
7. ***How this works***, by type (short sentences; a re-key changes no archive key,
   FORMAT.md:1278-1280, 1838-1840, and no recovery key, R38, so it promises neither — the outside
   review's finding 1):
   - YubiKey or password: "Re-keying gives the vault a new key in the same step, and every unlock
     method that stays gets it. An earlier copy of the vault file opened with {label} then reaches
     nothing in this vault through {label}; the recovery keys it held still do, until they are
     replaced. An archive that existed then stays open to such a copy, files added later included,
     until that archive is re-keyed with Re-key archive… on its page. A backup made before now is
     at the old key; the one taken afterwards is at the new one. The vault refuses a removal that
     would leave fewer than 2 unlock methods."
   - Recovery key: "Re-keying gives the vault a new key in the same step, and every unlock method
     that stays gets it. An earlier copy or backup opened with {label} then reaches nothing in
     this vault through {label}; the other recovery keys it held still do, until they are
     replaced. An archive that existed then stays open to it, files added later included, until
     that archive is re-keyed with Re-key archive… on its page. The backups made while {label} was
     an unlock method still open with it, and this vault can still import archives from them;
     where you keep them is yours to decide. The vault refuses a removal that would leave fewer
     than 2 unlock methods." (It told the user to destroy those backups; FORMAT §18.2 keeps an old
     backup for what a merge restores, FORMAT.md:1838-1842, and the Forget and Delete dialogs
     promise that the last backup can open an archive, strings.ts:1500; critique 2, UX 5.)
8. **The foot band**, the act first and the way out last (APP.md:3346-3350): the primary, a
   `type="submit"` danger-fill button (the destructive one, as now, KeysPage.svelte:607), naming
   what the press does, the backup included while ticked: **Remove and re-key**, **Remove unlock
   method** (the present primary, strings.ts:1547), **Remove, re-key and back up**, **Remove,
   then back up**; then *Cancel*. A long label takes a row of its own, as the foot band wraps
   (APP.md:3346-3348); no primary ends with "…" (strings.test.ts:142-148).

The dialog's height, by estimate from app.css and not measured: title, two body lines, the
question with its four lines, a four-line info line, the backup block and *How* closed come to
about 580 px at the 460 px box, inside a 768 px window; on a shorter one the body scrolls and the
foot band stays (app.css:1781-1794), as every dialog's does.

`doRemove()`, the dialog's submit: take the answer and the tick; close the dialog
(`removing = null`); with the tick, `pickBackup()` (KeysPage.svelte:152-154) and return on "" (a
Save dialog cancelled at the press removes nothing); record the act for the end's follow-up
(`store.lastAct = {act: "remove", type, rekey}`, 3.5, 3.6); then `beginWith((b) =>
Keys.RemoveSlot(id, answer === "rekey", b), path)` (3.2). `openRemove(m)` (the card's *Remove…*,
today `removing = m` at 413) resets the answer to "rekey" and the tick to its rule for that
answer.

The greyed *Remove…* on a YubiKey's card (the invariant's refusal, KeysPage.svelte:104-108) takes
a title that says what to do, ruling 3's path made findable: "To remove this YubiKey, add a new
one or a recovery key first: the vault always keeps at least 2 unlock methods."
(`vaultCopy.ruleYubiKey`). Both ways serve: a new YubiKey, or, with none at hand yet, a second
recovery key added through the switch (3.4; critique 3, M6). Either makes the YubiKey removable on
every vault where the title can stand: one YubiKey and one recovery key, or YubiKeys alone. The
other cards keep `vaultCopy.rule`.

### 3.2 (b) The password's change, turning it on, and the Re-key dialog

EntangledDialog's modes `change` and `on` (EntangledDialog.svelte:26-50) become forms with a
question; `off` stays as it is (its primary is a standard button so that nothing suggests it,
ruled 2026-10-18, and it asks nothing, since turning the password off withdraws nothing a re-key
could take back: the file then opens without it).

**Change** (DESIGN §5 rules out the technical question, "Also rotate the VMK?", DESIGN.md:399-400,
and gives a password change "its own wording", 406-407), top to bottom:

1. Title unchanged, "Change the password for every YubiKey?" (strings.ts:1577). The body says
   what authorises the act, which the present "none needs to be present" (strings.ts:1578) did
   not: the change proves an unlock method first, a YubiKey wherever the vault has one
   (keys.go:285) — the outside review's finding 5. New body: "Every YubiKey asks for the new
   password from now on; one unlock method proves it is you first, and the other YubiKeys need
   not be present. You choose the password in the next step."
2. The question: "Why are you changing the password?" over *Someone else may know it, or I am not
   sure* (first, pre-selected, re-keys) and *Only I have ever known it*, with the two second lines
   of 3.1. DESIGN.md:408-409's default, "Default to rotating on any password change", is the
   pre-selection; the user who wants no re-key gives a fact they can answer.
3. The limit: "Re-keying protects the future, not the past: whatever someone could open until now,
   they may already have copied. If an earlier copy of the vault file may be out there, whoever
   opened it with a YubiKey and the old password could also read the recovery keys: replace them
   afterwards with Replace recovery keys… on this page." The second sentence only while the vault
   has a recovery key, as in 3.1.
4. The backup block of 3.1, item 6, ticked on the re-keying answer.
5. *How this works*: first the disclosure of 2026-09-07, said truly (the outside review's second
   reading, finding 11: it says "The old password is not asked for", strings.ts:1579, while the
   proof with a YubiKey asks the vault's current password with its PIN, keys.go:285-290, as the
   acceptance walkthrough's step 5 shows): "This dialog has no field for the old password: whoever
   reaches this page has already opened the vault, and could add an unlock method of their own.
   Proving it is you with a YubiKey still asks for the current password with its PIN." Then:
   "Re-keying gives the vault a new key in the same step. An earlier copy of the vault file opened
   with a YubiKey and the old password then reaches nothing in this vault through them; the
   recovery keys it held still do, until they are replaced. An archive that existed then stays
   open to it until that archive is re-keyed with Re-key archive… on its page."
6. The accent primary (as now, EntangledDialog.svelte:45-48), `type="submit"`: **Change and
   re-key**, **Change password**, **Change, re-key and back up**, **Change, then back up**; then
   *Cancel*.

**Turn on** (a copy of the vault file made before the switch keeps opening with a YubiKey alone
and yields the VMK, DESIGN.md:384, 391-395), top to bottom:

1. Title unchanged, "Require a password with every YubiKey?"; the body's promise narrowed to what
   is true of this file: "Every YubiKey will ask for this password as well as its PIN, so a stolen
   YubiKey alone cannot open this vault file. The recovery key and a password unlock method do not
   change." (it said "the vault", strings.ts:1571).
2. The question: "Could the vault file have been copied before now?" over *Yes, or I am not sure*
   (first, pre-selected, re-keys) and *No, it was never copied*, with the second lines of 3.1. A
   backup holds no YubiKey, so only copies of the vault file matter here, which the legend says.
   Users generally cannot know that a file was never copied, which is why the uncertain answer is
   the default (the outside review's Q5).
3. The limit: "Re-keying protects the future, not the past: whatever someone could open until now,
   they may already have copied. Whoever opens an earlier copy of the vault file with a YubiKey
   alone can also read the recovery keys: if such a copy may exist, replace them afterwards with
   Replace recovery keys… on this page." The second sentence only while the vault has a recovery
   key.
4. The backup block, ticked on the re-keying answer.
5. *How this works*: the present text (strings.ts:1572-1573), then: "Re-keying gives the vault a
   new key in the same step. An earlier copy of the vault file, which opens with a YubiKey alone,
   then reaches nothing in this vault through that YubiKey; the recovery keys it held still do,
   until they are replaced. An archive that existed then stays open to it until that archive is
   re-keyed with Re-key archive… on its page."
6. The accent primary, `type="submit"`: **Turn on and re-key**, **Turn on password** (as now),
   **Turn on, re-key and back up**, **Turn on, then back up**; then *Cancel*.

`go()` in modes change and on: take the answer and the tick; close; with the tick, `pick()` and
return on ""; record the act for the end (`store.lastAct`, 3.6); then `beginWith((b) =>
Keys.ChangeEntangledPassword(rekey, b), path)` or `beginWith((b) => Keys.SetEntangled(true, rekey,
b), path)`; off calls `begin(() => Keys.SetEntangled(false, false, ""))`. EntangledDialog's props
gain `pick: () => Promise<string>`, `beginWith` and `note: (a: LastAct) => void` from KeysPage;
`begin` stays for off.

**The Re-key dialog** keeps its title. Its body says what authorises it, like the change's (the
outside review's finding 5; it said "no YubiKey is needed for this", strings.ts:1535, while
`RotateNow` proves with a YubiKey wherever there is one, keys.go:285, 868): on a vault with a
YubiKey, "Every unlock method gets the new key now; one of them proves it is you first, and the
other YubiKeys need not be present. Backups made before today keep opening with the methods they
had."; on a vault without one, the first sentence ends at "first" (a vault of passwords and
recovery keys has no other YubiKeys to speak of; critique 2, UX 14f). Its *Last backup* fact,
reason and tick become the shared block (so the tick reads *Export a backup afterwards*, is drawn
only while the vault has a recovery key, and is ticked whenever drawn, the dialog's one act being
a re-key, with the re-key's reason; today it is always drawn, KeysPage.svelte:624, and an export
of a vault with none fails with "A vault always keeps a recovery key.", strings.ts:238); its
primary is **Re-key vault** or **Re-key, then back up** (it was *Back up, then re-key*,
strings.ts:1541: the order follows ruling 1). `doRotate()` becomes `beginWith((b) =>
Keys.RotateNow(b), path)` after the same picker, and `rekey()` (KeysPage.svelte:224-233) goes,
which is the fix of the finding in section 1.

KeysPage gains, beside `begin` (KeysPage.svelte:75-87), which is unchanged:

```ts
  // An act that may take a backup after its change (APP.md §13): the file
  // is kept for the status bar's done line when the dialog closes on a Done
  // that wrote it (CeremonyState.backedUp). Start again repeats the act with
  // the same file: an act that failed changed nothing and wrote nothing.
  function beginWith(run: (backup: string) => Promise<unknown>, backup: string) {
    backupTo = backup;
    return begin(() => run(backup));
  }
```

`exportedTo` (KeysPage.svelte:160) becomes `backupTo`, and `closeCeremony` (328-335) shows
`backupDone(backupTo)` when the ending ceremony is at Done and is kind "export" or has `backedUp`.

### 3.3 (c) *Replace recovery keys…* (ruling 2)

**(a) Where it lives and its name.** In the head of *Unlock methods*, after *Add an unlock
method…* and *Re-key vault…* (KeysPage.svelte:382-390): **Replace recovery keys…**, a small
standard button with the recovery icon (`#i-recovery`, KeysPage.svelte:90). Why there: the page's
rule is that the vault-wide acts stand in the section's head, apart from the cards, "so none reads
as a card's" (KeysPage.svelte:377-378; APP.md:2194-2196), and this act replaces every recovery key
at once — a button on one recovery key's card would promise to replace that one. Why the name: it
is the verb table's shape (a command names what happens, "…" when a dialog follows,
strings.ts:33-36), the owner's words, and the same noun the cards' own *Show recovery key…* uses;
the plural stands whatever the count, as a command names the act, and the dialog's title counts
(b). The head then holds three buttons; their widths, by estimate (about 150, 110 and 165 px at
the small size, 12 px gaps) beside the title and the count, fit the 760 px column (app.css:149)
with room, and the head is given `flex-wrap: wrap` with a row gap in KeysPage's style so that a
narrower window wraps the acts under the title rather than overflowing (not measured; section 6).

Greyed, with its reason as the title (APP.md §6, Controls: a greyed control keeps the pointer for
it): locked ("needs unlock", as its neighbours, KeysPage.svelte:62); Tampered
(`vaultCopy.changesOff`); a vault with no recovery key ("This vault has no recovery key to
replace.", `vaultCopy.noRecoveryToReplace`); a vault whose methods are all recovery keys ("Add a
YubiKey or a password first: a new recovery key can be shown again only through one.",
`vaultCopy.replaceNeedsProtector`; 2.3 says why). Words of its own, not `vault.setup_needed`'s
("This vault has only its recovery key so far. Finish setting it up first.", strings.ts:229),
which are wrong where a removal left two recovery keys: the vault was set up, and *Finish setting
up* is refused there while its password is on (critique 2, UX 2). The accent stays *Add an unlock
method…*'s (one accent per surface, KeysPage.svelte:383-387).

**(b) The dialog** — a form, *Replace recovery keys* opened by the head's button or by an end's
follow-up (3.6), centred, 460 px. Top to bottom, n being the vault's recovery keys:

1. **Title**: "Replace the recovery key?" (n = 1), "Replace the {n} recovery keys?" (n > 1).
2. **Body**, two sentences, the consequence for the user's files first, then what still opens
   (the template, strings.ts:44-49): "Enfold makes a new recovery key for you to keep, and the old
   one stops unlocking this vault. Copies and backups of the vault made before now still open with
   the old one." (n > 1: "Enfold makes {n} new recovery keys for you to keep, one for each, and
   the old ones stop unlocking this vault. Copies and backups of the vault made before now still
   open with the old ones.")
3. **When it is needed**, the info line with its icon (as in 3.1, item 5), by count (critique 2,
   UX 11a): n > 1, "Replace them when an earlier copy of the vault file, or a backup, may have been
   opened by someone else: whoever did could read every recovery key in it, and those keep opening
   this vault until they are replaced. If that copy was opened with a YubiKey or a password,
   remove that unlock method first, and remove any unlock method you did not add."; n = 1,
   "Replace it when an earlier copy of the vault file, or a backup, may have been opened by
   someone else: whoever did could read the recovery key in it, which keeps opening this vault
   until it is replaced. If that copy was opened with a YubiKey or a password, remove that unlock
   method first, and remove any unlock method you did not add." With the vault's password on,
   " Change the password too: the vault's own record of it was readable the same way." (R38,
   FORMAT.md:733-736; the outside review's finding 4), left out when the chain the dialog was
   opened in has already re-keyed with a password change or with turning the password on
   (`from.changed`, below; critique 3, M1): a change took back the record an earlier copy held,
   and a copy made before the switch was turned on held none. Why the
   third sentence (critique 2, UX 11c and F7): a replacement replaces recovery slots only, so a
   YubiKey or a standalone password the copy was opened with keeps opening this vault whatever the
   recovery keys become, and a YubiKey added by whoever held an exposed recovery key survives the
   replacement (2.3).
4. **What to be ready for**, a line in the body ink: "Have somewhere to keep each new recovery key
   before you start: print it, save it as a text file, or write it down. Nothing changes until you
   have kept every one and confirmed. Enfold locks after {m} minutes without input, which ends the
   replacement with nothing changed: printing or saving is quicker than writing 48 digits by
   hand." m is the idle time the session runs on: the status's `idleEffectiveMinutes`, the core's
   clamp applied (`replaceMinutes`, lib/rekey.ts), the default only while the status names none.
   As built, not the stored minutes this item first named (`s.idleMinutes || IDLE_DEFAULT`): the
   core runs a stored value out of range on the default, so a line that said it would promise time
   the session does not give (the outside review of the diff, finding 9). Why (critique 2, UX 10):
   idle counts real input only (session.go:735-768), the idle choices go down to 2 minutes
   (KeysPage.svelte:133), and 48 digits written by hand is minutes without input. Pausing the idle
   lock while a key is on screen would leave a recovery key unattended, so the line warns and the
   keep view offers *Stay unlocked* in the deadline's last minute ((d) 2).
5. **The backup**, the shared block (3.1, item 6) with this act's reason and the tick ALWAYS ticked:
   "Backups made before now open only with the old recovery keys; one made afterwards holds the new
   ones." (for one key, "Backups made before now open only with the old recovery key; one made
   afterwards holds the new one."). Why always: after the replacement every earlier backup opens
   only with keys the user is replacing (R28: a backup holds the recovery slots alone,
   FORMAT.md:526-528), so without a new one no backup opens with a key the user still keeps. The
   user may clear it.
6. ***How this works***: "Enfold makes the new keys first and shows each one in turn. When you
   confirm, one step adds them, removes every old recovery key and re-keys the vault, so the copy
   of each new recovery key the vault keeps is never readable with an old one. An earlier copy or
   backup then reaches nothing in this vault through its recovery keys; an archive that existed
   then stays open to it, files added later included, until that archive is re-keyed with Re-key
   archive… on its page. Each new recovery key takes the name of the one it replaces, so you know
   where it goes." With the vault's password on, " Such a copy also held the vault's record of
   the password, which only changing the password replaces." (critique 2, UX 11b: until the
   password changes, the copy's `K_P` still serves as the second factor beside any YubiKey still
   enrolled, FORMAT.md:733-736.)
7. **The foot band**: the accent primary, `type="submit"` — **Replace recovery keys**, or **Replace
   recovery key** for one, or **Replace, then back up** while ticked — then *Cancel*. Accent, not
   danger-fill: the act protects, and the old keys it ends are the ones the user is replacing.

On submit: close; with the tick, `pickBackup()` and return on "" (nothing begins); record
`store.lastAct = {act: "replace"}`; `beginWith((b) => Keys.ReplaceRecoveryKeys(b), path)`. The
dialog opens with the focus on its submit (APP.md:3196-3201: a form with no field and no
question). ReplaceDialog's props are `onclose`, `beginWith` and, when an end's follow-up opened
it, `from: { after: AfterFacts; changed: boolean }`: the final state's committed facts, from which
it reads its count and whether the password is on, rather than from the page's slots and switch,
which may still be the reads from before the act (critique 3, N10), and the chain's `changed`, a
re-keying password change or turning-on earlier in it (M1). `replaceFacts` (3.5) decides all
three.

**(c) How many keys**: as many as the vault has (2.7: the arrangement the labels name, and the
invariant by construction). The dialog says the number in its title and body, so the user knows
how many sheets to prepare before pressing.

**(d) The ceremony, as the user sees it.** In the ceremony dialog titled "Replace the recovery
keys" ("Replace the recovery key" for one; `vaultCopy.ceremonyTitle.replace`, by `KeepCount` or
the cards' count):

1. *The proof*: the YubiKey's wait, PIN (with the vault's password when on) and touch, or the
   typed password, exactly as every act's (CeremonyPanel.svelte:254-341); "Checking that it is
   you…" after it.
2. *Each new key*, the recovery key's dialog taking over from the act's, as it already does after
   an enrolment (APP.md:3838-3839): KeysPage mounts `RecoveryReveal` in keep mode while the
   ceremony is at `StepKeepKey` (in place of the ceremony dialog; App.svelte's mount, which needs
   `StepRecovery` with no prompt, App.svelte:58, does not fire). The dialog is the one the user
   already knows — 520 px, the eight groups, *Print…* and *Save as a text file…* beside the
   digits, *Check what I wrote*, the bars that say each way taken (RecoveryReveal.svelte:160-242)
   — with these differences, carried by two new props, `keep?: { promptId: string; index: number;
   count: number; replaces: string }` and `oncancel?: () => void` (without `keep` it is the dialog
   it is today):
   - its title, "New recovery key 1 of 2" (one key: "Your new recovery key"), the new key's ID as
     the heading's label (RecoveryReveal.svelte:161-163);
   - first, a caption in the second ink: "Replaces {label} · {old ID}." — the old key's label and
     ID from `OldIDs` and the cards, so the new sheet goes where the old one is;
   - its lead's first sentence, "Once you confirm, this recovery key opens the vault file by
     itself, and backups made from then on. Keep it from others." ("alone" read as "only this
     key", critique 3, N6; until the commit it opens nothing; the
     ordinary showing's "without your YubiKey", strings.ts:2121, is wrong on a vault with none, so
     keep mode does not inherit it; critique 2, UX 14f); the second unchanged (strings.ts:2122);
   - the digits' caption: "48 digits. Nothing changes until every new recovery key is kept and you
     confirm." (the present one promises a later showing, strings.ts:2124, which is true only
     after the commit);
   - its band: before any way is taken, *I have written it down* (standard) and *Cancel*. Each
     way counts at once, *I have written it down* included, with no second confirmation, as in
     every showing (ruled 2026-09-09, and held for the replacement on 2026-10-27, R-B: a user
     set on leaving is not stopped by a second click, and the act's own confirmation before its
     commit, step 3 below, is where every new key stands once more beside the one it replaces).
     Once a way is taken, **Next recovery key** (accent) — **Continue** for the last — and
     *Cancel*. The press answers the `kept` prompt (`submitSecret("kept", promptId, "")`), drops
     the handle (`Keys.DropRecoveryKey`, as `finish` does, RecoveryReveal.svelte:90-94) and
     clears the readback's fields; the next key's dialog then takes its place by the next event
     (the `{#key}` on the URL, as App.svelte:190-192 keys its mount).
   - **When the key cannot be shown** (critique 2, UX 9): a failed fetch of the one-time URL
     (RecoveryReveal.svelte:65-78), or a save answered `ceremony.stale_prompt` (the handle no
     longer held, 95-104), draws in place of the digits the danger bar "This recovery key could not
     be shown here. Nothing has changed: cancel, and start the replacement again." over a band of
     *Cancel* alone. The ordinary showing's words for that case ("Show it again from the Vault
     page", `revealCopy.gone`, `goneHow`, `notHeld`, strings.ts:2142-2144) are false before the
     commit, and its *Done*, like *I have written it down*, would answer `kept` for a key the user
     never saw, against 2.7's rule that a key never kept must never become the vault's.
   - **Stay unlocked**: in the idle deadline's last minute (`staysUnlocked`, foot.ts:117-128, the
     status bar's own rule, LayerFoot.svelte:49, 287), the band carries the subtle *Stay unlocked*
     (`footCopy.stayUnlocked`), which sends `Vault.Activity` as the status bar's does, since the
     modal dialog stands over the status bar. The press is real input, so the grant passes
     (session.go:735-768). The absolute cap still ends the act, as it ends every prompt.
   - Escape and the backdrop do nothing in keep mode, as in every showing of a recovery key
     (APP.md:2600; RecoveryReveal.svelte:160 passes `onclose={() => {}}`, and keep mode keeps it):
     *Cancel* is the one way out, and it cancels the ceremony (`Vault.CancelUnlock`). Why not
     Escape as *Cancel*, as every ceremony's dialog has it (APP.md:3300): keep mode holds the
     readback's fields, where an Escape is a reflex, and here it would cost every sheet written so
     far (critique 3, N7). The confirmation that follows is the act's own dialog, with no field,
     and Escape is *Cancel* there as in every ceremony.
3. *The confirmation*: the act's dialog returns, at `StepConfirm`, its view drawn by the panel
   (3.6): the lead "Ready to replace", then a label · value list with one row per key, the key's
   label and "3F7A-9C21 replaces 51D0-77AA" (`replaceCopy.pair`) — so the pairing and the place
   each sheet goes stand to the last check (critique 2, nit 17) — then the caption "The replaced
   keys stop unlocking this vault, and every new one works from now on. Check that you can read
   each new recovery key before you go on." (for one, "The replaced key stops unlocking this
   vault, and the new one works from now on. Check that you can read the new recovery key before
   you go on."). Its foot band: **Replace recovery keys** (accent;
   **Replace recovery key** for one), which answers the `confirm` prompt, and *Cancel*.
4. *The commit*: "Replacing the recovery keys and re-keying the vault…" ("Replacing the recovery
   key and re-keying the vault…" for one) on the neutral panel; then, with a backup, "Saving the
   backup…".
5. *The end*, on the neutral done view (3.6): the check and "Recovery keys replaced and vault
   re-keyed" ("Recovery key replaced and vault re-keyed"), then the receipt, the same one row per
   label ("Safe · 3F7A-9C21 replaces 51D0-77AA", from `NewIDs`, `OldIDs` and the labels; FORMAT
   §18.4: the sheets carry the IDs), then one line by what the backup did:
   - written (`backedUp`): "Destroy the replaced keys' sheets and files once the new backup opens
     (Test that a backup opens…). The backups made before still open with the replaced keys, and
     this vault can still import archives from them."
   - not asked for: an attention bar, "No backup holds the new keys yet, and every earlier one
     opens only with the replaced keys. Export one from Backups and copies, and destroy the
     replaced keys' sheets and files once it opens."
   - failed (`backupError`): the shared bar, "The backup was not saved. {why} Export one from
     Backups and copies.", then the line of the case before.
   - not taken because the retirement did not land (`backupError` `vault.retire_owed`): no bar of
     its own, the retirement's bar saying that the backup asked for waits too (3.6; critique 3, M7),
     then the line of "not asked for". Each line is in the singular for one key (`replaceCopy`'s
     `One` forms, 3.7; critique 3, N1: a count of one is singular everywhere, DECISIONS 2026-09-09).
     And, with the vault's password on and no re-keying change of it earlier in the chain, the
     follow-up *Change…* with "If an earlier copy of the vault file may be out there, change the
     password too: the vault's own record of it was readable the same way." (3.6), since a
     replacement replaces recovery slots only; otherwise nothing. And *Close*. Closing on a Done
     that wrote a backup shows its done line in the status bar (2.5). Revision 2 told the user to
     destroy the backups made before as well; that is struck (critique 2, UX 5). FORMAT §18.2 keeps
     an old backup for what a merge restores, forgotten and deleted records (FORMAT.md:1838-1842),
     *Import archives from a backup…* opens it through this vault's `vmk_history` rather than its
     recovery key (APP.md §13, Merge records), and the Forget and Delete dialogs promise that the
     last backup can open an archive (strings.ts:1500). The end says what is true of those backups
     and leaves them to the user; a ruling to destroy them would be one against FORMAT §18.2's
     stated use.

**(e) What the page says after.** The cards show the new IDs at once (the facts are cached at the
act's end, 2.7, and the page reads the slots again on close, KeysPage.svelte:328-335). The backups
card: while no backup recorded here holds a current recovery key (`BackupStatus.KeysReplaced`),
its status is the *behind* bar, "**Backed up <since>.** It opens only with recovery keys this vault
no longer has.", which stands for the counts and makes *Export backup…* the accent; a replacement
also re-keyed the vault, but this sentence says the more pressing fact, so it wins over the
re-key's (3.7). After a new backup it reads current again.

**When the act ends without replacing anything after keys were shown, or may have** (the outside
review's second reading, finding 9: one classification, `replaceResult`, 3.5, for the dialog and
for the Vault page). A cancel at a keep step or at the confirmation, a lock trigger, the prompt's
deadline, or a failure before the commit's flip with any code but `vault.indeterminate` and
`vault.conflict`, is *never added*: the kept sheets name keys that never existed. A failure with
`vault.indeterminate` or `vault.conflict` is *unknown*: the first comes from a superblock write
whose outcome the handle cannot know, and the second can come from the retiring commit after the
replacement itself committed (2.4, step 5), so either may have left the new keys the vault's, and
the page never says to destroy them then. The ceremony dialog closes on a cancel (a cancelled
ceremony is dropped at once, frontend/src/lib/state.svelte.ts:1084-1086) and the lock screen may
be up, so the page cannot rely on the dialog to say it: the store keeps the IDs it saw at
`StepKeepKey` for a kind-"replace" ceremony and raises `store.replaceNotice` through
`replaceNotice` (3.5). It is kept when the vault leaves Unlocked — an ID is not secret, a
recipient ID being plaintext in the vault file (FORMAT.md:1006) — so a replacement ended by a lock
or by Broken is still named at the next unlock. The Vault page draws it in its notice slot
(KeysPage.svelte:361-375), the attention bar with its close button, until closed or until the next
replacement begins:

- never added: "Nothing was replaced. Recovery keys 3F7A-9C21 and 0B44-1E07 were shown but never
  added: destroy what you kept of them." (one key: "Recovery key 3F7A-9C21 was shown but never
  added: destroy what you kept of it.");
- unknown: "Recovery keys 3F7A-9C21 and 0B44-1E07 were shown, and Enfold could not tell whether
  they replaced the old ones. Reopen the vault; the Vault page then lists the recovery keys it
  has. Keep every sheet until then." (one key: "Recovery key 3F7A-9C21 was shown, and Enfold could
  not tell whether it replaced the old one. Reopen the vault; the Vault page then lists the
  recovery keys it has. Keep both sheets until then."). Words for both causes, a save that could
  not be confirmed and a conflict with another writer, whose own words differ (strings.ts:248-249;
  critique 3, N2). It settles itself once the slots are read after the
  reopen and an unlock: every ID listed, it is cleared, since the replacement landed and the
  cards say so; none listed, it becomes the never-added notice.

A failed view in the dialog adds the same under its bar (3.6): for never added, "Nothing was
changed." and `keysNeverAdded`, with *Start again*, which begins a new act with new keys; for
unknown, Broken's own words and `keysWorkIfListed`, "The new recovery keys work only if the Vault
page lists their IDs after you reopen it: keep every sheet until you have looked." The IDs decide
it either way.

**(f)** The events, the fields, the bound method and the steps are 2.1, 2.5 and 2.7; the mock is
3.10.

**(g)** The copy is 3.7; **(h)** the tests are 2.6 (keystore 11-24 and 32, app 19-28, 37 and 39)
and 3.9.

**(i) How the other acts point to it.** In their info line (3.1, item 5; 3.2), by the act's
on-screen name and when it is needed; and in their end (3.6), as a button the user can press at once
beside the sentence that says when. The ends form one chain, the password first (critique 2, UX 4):
after a re-keying removal on a vault whose password is on, the end offers *Change…* (the password's
dialog); after a re-keying password change, and after a re-keying removal or turning the password on
with it off, the end offers *Replace recovery keys…*; where a YubiKey's removal left the vault with
recovery keys alone, the end offers *Add an unlock method…* first, since the replacement is refused
there (2.3), and while the vault has recovery keys alone no end offers anything else, the end of a
recovery key added from that button included (critique 3, M3); and the replacement's end offers
*Change…* where the password is on and no re-keying change of it came before in the chain, so that a
user who began with the replacement is led to the password too, and otherwise nothing.

**A chain is the acts begun from one another's ends** (the outside review's second reading,
finding 6). An act that re-keys, begun from the page itself — the head, a card, a dialog opened
directly — starts a new chain; an act begun from an end's button continues the chain that end
belongs to; a lock ends it, since it closes every dialog and no end's button outlives it (3.5,
`chainFor`). Within a chain no end points back to recovery keys the chain's replacement made, nor
to a password change the chain made, so the chain never loops: removal, change, replacement ends
there, and so does replacement, change. Outside a chain the advice is whole: a password change
begun later on its own answers a later exposure, and nothing about keys made earlier in the same
window says they were not exposed since. (Revision 3 suppressed the replacement whenever every
recovery key had been made by a replacement since the page loaded, a fact that outlived a lock
and a new compromise.)

Each act is its own commit, not one repair transaction (the review's Q5 position): until the
chain's last act lands, the recovery keys an earlier copy held still open the vault, so whoever
holds one and watches the vault file between the steps reads what it holds then, the keys of
archives created in between included, which no later step changes. The chain's order, the
password before the replacement where the password is on, and each end's sentence saying when its
step is needed, are what this design does about it; a single act doing all three is follow-up 6's
kind of change, not this one. Pressed, a button closes the ceremony dialog and opens its act's
dialog. The password is never folded in: the change is its own act with its own question (3.2).
The status bar's done line does not carry the command (2.5: 6 s, and one of two link kinds).

### 3.4 (d) The recovery-key proof, and the acceptance scenario (ruling 3)

**On screen.** In the ceremony dialog of *Add an unlock method…*, while its proof waits for a
YubiKey — the insert view with "Insert a YubiKey that already unlocks this vault" over the vault's
YubiKeys by name (CeremonyPanel.svelte:254-262) — a subtle small button under the lead: **Use
recovery key instead**, the lock screen's own words for the same switch
(`lockCopy.useRecoveryInstead`, strings.ts:1839; Lock.svelte:793). At *No match* the dialog draws
what the lock screen draws there (Lock.svelte:796-809; critique 2, UX 13): the step's own body
(`stepText(StepNoMatch).body`) in place of "Try another YubiKey." — a caption that exists because no
button was offered (strings.ts:124-127) — and the accent **Use recovery key** in the foot band
before *Cancel* (`dialogFoot` gains the button, 3.6). Both are drawn only when the act is an
enrolment of a YubiKey or of a recovery key (2.3), begun without the switch, on a vault that has a
YubiKey and a recovery key — never in the enrolment's second half (the new YubiKey's wait), never
for another act — which one predicate decides, `offersRecoverySwitch` (3.6; critique 3, M5).
APP.md's rule for *No match* — only an unlock turns to the recovery key there, and a ceremony in a
dialog holds *Cancel* alone (APP.md:1662-1664) — gains this one exception by name, and keeps the
rule for every other case (4.1; the outside review's second reading, finding 10). Pressed: the page
cancels the running ceremony (`Vault.CancelUnlock`), waits until the store has dropped it (as the
lock screen's `untilLocked` waits, Lock.svelte:433-444, with the same 8 s bound), and begins the act
again with the recovery key. `LastAct` carries no label, and `again` re-runs the act as it was
begun, so KeysPage keeps beside `again` a second closure that the enrolment's start makes with its
kind and label, `toRecovery = () => begin(() => Keys.BeginEnroll(kind, label, true), true)`, and the
switch calls it (critique 2, nit 19). Both `again` and `toRecovery` keep the act's `fromFollow` and
`chain` as its first begin recorded them, the switch setting `lastAct.withRecoveryKey` and nothing
else, so that an act begun again continues the chain it began in rather than starting one (critique
3, N9). `keep` set, so the dialog stays up: KeysPage holds the last ceremony state while it
switches, drawing it until the new ceremony's first event, as *Start again* does
(KeysPage.svelte:68-87). The proof is then the recovery view the dialogs already have
(CeremonyPanel.svelte:308-314), its note "Your recovery key proves it is you, since no YubiKey of
this vault is at hand. The new YubiKey comes next." ("The new recovery key comes next." for a
recovery key). The enrolment goes on as ever, minus the swap: no YubiKey proved, so none is asked
out first. A YubiKey's end adds the next step under the title, a line in the second ink: "Now remove
the lost YubiKey: Remove… on its card asks why, and re-keys the vault when it was lost." A recovery
key's enrolment ends, as every one does, in the recovery key's dialog, which shows the new key
(keys.go:379-401). Its digits' caption, which today promises a later showing "from the Vault page:
unlock, then prove a YubiKey or a password once more" (strings.ts:2124), reads instead "48 digits.
They can be shown again from the Vault page only after you add a YubiKey or a password and prove
it." (`revealCopy.digitsLineLater`, while `store.lastAct` is a recovery key's enrolment begun with
the switch), since none is at hand to prove with. No next-step line follows it, and none is needed:
*Remove…* on the lost YubiKey, whose greyed title sent the user here, is enabled once the dialog
closes (critique 3, M6).

A removal of the vault's only YubiKey answered "lost" needs no switch: the core proves with a
recovery key at once (2.3), and the view's note is "One of this vault's recovery keys proves it is
you, since the YubiKey being removed is not asked for." The note is chosen by its cause, not by
the act's kind (critique 2, UX 8; `recoveryNoteOf`, 3.5): `without` set, the removal's; the
switch, the enrolment's; any other act of a vault whose methods are all recovery keys, which
already proves with one (keys.go:268-283), "This vault has only its recovery keys, so one of them
proves it is you.", with " The new unlock method comes next." for an enrolment. Chosen by kind, as
`recoveryNote` is today (CeremonyPanel.svelte:311), adding a password to such a vault would read
"since no YubiKey of this vault is at hand. The new YubiKey comes next.", both false.

**The acceptance scenario**, end to end on screen: a vault of one YubiKey ("Desk YubiKey") and one
recovery key ("Recovery key", ID 51D0-77AA), the password on or off; the YubiKey is lost.

1. **Unlock with the recovery key.** The lock screen at rest: *Unlock with recovery key*
   (Lock.svelte:752, strings.ts:1840), the recovery key's cells naming "51D0-77AA · Recovery key",
   the 48 digits, *Unlock*. The vault opens (no change here: APP.md:1697-1703).
2. **The Vault page.** *Remove…* on "Desk YubiKey" is greyed; its title says "To remove this
   YubiKey, add a new one or a recovery key first: the vault always keeps at least 2 unlock
   methods." (3.1).
3. **Add the new YubiKey.** *Add an unlock method…* → "YubiKey (PIN and touch)", the name "Travel
   YubiKey" → *Add unlock method*. The dialog "Add an unlock method" opens, by design, on the wait
   for a YubiKey of the vault: "Insert a YubiKey that already unlocks this vault" over "Desk
   YubiKey. The new unlock method comes next.", and under it *Use recovery key instead*. Pressed:
   the recovery view with its note; the 48 digits of 51D0-77AA; *Continue*. Then "Now insert the
   new YubiKey"; the new YubiKey's PIN (the management key's if it asks), its touch on the green;
   "Unlock method added", and under it "Now remove the lost YubiKey: Remove… on its card asks why,
   and re-keys the vault when it was lost."; *Close*. (The vault's password, when on, is not
   asked: the new YubiKey is wrapped from the kept K_P, keys.go:403-407.)
4. **Remove the lost one.** *Remove…* on "Desk YubiKey" is now enabled. The dialog "Remove Desk
   YubiKey?": "Desk YubiKey stops unlocking this vault now. An earlier copy of the vault file still
   opens with it; a backup never held it."; "Why are you removing this YubiKey?" with *Lost,
   stolen, or I am not sure* checked; the info line ending "…replace them afterwards with Replace
   recovery keys… on this page." (with the password on, "…change the password afterwards, then
   replace the recovery keys with Replace recovery keys… on this page."); the backup block,
   ticked, since the answer re-keys and a recovery key remains; **Remove, re-key and back up**
   (or **Remove and re-key** with the tick cleared). The ceremony "Remove an unlock method":
   "Insert a YubiKey that already unlocks this vault" over "Travel YubiKey." — Desk YubiKey left
   out; Travel YubiKey's PIN (and the vault's password when on); its touch; "Removing the unlock
   method and re-keying the vault…"; (with a backup, "Saving the backup…"); then the end, on the
   neutral done view since it carries a follow-up (3.6): "Unlock method removed and vault
   re-keyed", and under it, with the password off, "If an earlier copy of the vault file may be
   out there, replace the recovery keys too." with the button **Replace recovery keys…**; with the
   password on, "If an earlier copy of the vault file may be out there, change the password, then
   replace the recovery keys." with the button **Change…**.
5. **With the password on, change it.** The end's *Change…*, or *Change…* beside *Password with
   every YubiKey*: "Why are you changing the password?" with *Someone else may know it, or I am
   not sure* checked, the backup block ticked, **Change, re-key and back up**; Travel YubiKey's
   proof with the old password; the new password twice; "Password changed and vault re-keyed",
   and under it "If an earlier copy of the vault file may be out there, replace the recovery keys
   too." with **Replace recovery keys…**.
6. **Replace the recovery key.** The end's button, or the page head's *Replace recovery keys…*. The
   dialog "Replace the recovery key?" with its body, its info line (without "Change the password
   too" when opened from step 5's end), its ready line naming the idle time, and its backup block,
   ticked; **Replace, then back up**; the Save dialog names the file. The ceremony "Replace the
   recovery key": Travel YubiKey's wait, PIN (and the new password when on) and touch. Then "Your
   new recovery key", headed with its ID (say 3F7A-9C21), "Replaces Recovery key · 51D0-77AA.";
   *Print…*, *Save as a text file…* or *I have written it down*, each of which counts at once;
   **Continue**. Then "Ready to replace": *Recovery key* · "3F7A-9C21 replaces 51D0-77AA", "The
   replaced key stops unlocking this vault, and the new one works from now on. Check that you can
   read the new recovery key before you go on."; **Replace recovery key**. Then "Replacing the
   recovery key and re-keying the vault…", "Saving the backup…", and "Recovery key replaced and
   vault re-keyed" with the same row, "Destroy the replaced key's sheet and file once the new backup
   opens (Test that a backup opens…). The backups made before still open with the replaced key, and
   this vault can still import archives from them."; no follow-up, the password having been changed
   in this chain (with the password off there is nothing to change); *Close*; the status bar:
   "Backup saved to …" [*Show in Explorer*]. Begun from the page's head instead, the replacement
   starts a chain of its own: its dialog then says to change the password too, and its end offers
   *Change…*, which a user who has just changed it closes (3.3 (i)).

With the password off, step 5 is skipped: step 4's end offers *Replace recovery keys…* at once.
Without a new YubiKey at hand yet, step 3 adds a second recovery key instead, by the same switch;
step 4 then proves with a recovery key, since the YubiKey removed was the vault's only one, and
its end offers *Add an unlock method…* for the YubiKey bought later (2.3, 3.6; app test 40).
Done in another order — the replacement first, from the page's head — the replacement's end
offers *Change…* with the password on, and the change begun from it ends with nothing more, its
chain having replaced the recovery keys already (3.5, `followUp`); a password change begun later
on its own, from its card, offers the replacement again, since it answers a new exposure (3.3 (i)).

What is then true (2.6, app test 26; keystore tests 21 and 26-28): Desk YubiKey opens nothing in
the vault file, which holds no record of it, and no registry sealed before step 4 is left in it;
51D0-77AA opens nothing in the vault file and no backup made after step 6; an earlier copy of
the vault file opened with Desk YubiKey or 51D0-77AA reaches no archive created after step 4 and
no recovery key or password of now; an archive that existed before stays open to such a copy
until it is re-keyed on its page, which every dialog's *How this works* says. Steps 4 to 6 each
prove with an unlock method the user has; step 3 opens, by design, on the wait for the lost
YubiKey and offers the switch under it.

### 3.5 Pure logic, for tests (a new frontend/src/lib/rekey.ts)

KeysPage has no component test, so the decisions go into a pure module, as lib/backup.ts and
lib/foot.ts hold theirs:

```ts
// The two answers of every question that may re-key (DESIGN §5).
export type Answer = "rekey" | "keep";
// The acts whose dialogs offer the backup (APP.md §13).
export type Act = "remove" | "password" | "on" | "rotate" | "replace";
export const WEEK = 604800;
// Owed: none recorded here, the last over a week old, made before the vault
// was last re-keyed, or holding only replaced recovery keys (BackupStatus).
export function backupOwed(o: { lastExportAt: number; nowMs: number; vaultRekeyed: boolean; keysReplaced: boolean }): boolean;
// Possible: a backup holds the recovery keys alone, so one can be taken
// after the act only while a recovery key remains.
export function backupPossible(o: { recoveryKeys: number; removing?: string }): boolean;
// Ticked: an act that re-keys — the re-keying answer, a re-key, a
// replacement — since its own re-key makes every earlier backup behind
// (critique 2, UX 1); a recovery key's removal on either answer, since the
// backups made while it was enrolled hold it; otherwise while owed. rekey
// is the answer (true for rotate and replace).
export function backupTicked(o: { owed: boolean; act: Act; rekey: boolean; removing?: string }): boolean;
// The reason under the Last backup fact, by what ticks it (3.1, item 6):
// the re-key's, the removed recovery key's, the replacement's, the owed
// one's (strings.ts backupWhy), or undefined; the replacement's in the
// singular for a count of 1.
export function backupReason(o: { owed: boolean; act: Act; rekey: boolean; removing?: string; label?: string; count?: number }): string | undefined;
// The primary, naming what the press does (strings.ts dialogCopy.primaries);
// count words a replacement of one key in the singular.
export function primaryOf(act: Act, rekey: boolean, backup: boolean, count?: number): string;
// The last protector: removing this YubiKey leaves no YubiKey and no
// password (3.1, item 3b).
export function lastProtector(slots: { recipientId: string; type: string }[], removing: { recipientId: string; type: string }): boolean;
// A repair chain (the outside review's second reading, finding 6): the acts
// begun from one another's ends. replaced is the IDs a replacement in this
// chain made; changed, whether a re-keying password change or turning-on in
// it has ended Done.
export interface Chain { id: number; replaced: string[]; changed: boolean }
// What the page began, for the end's follow-up (3.6). type: the method a
// removal takes, or the kind an enrolment adds ("token", "recovery",
// "password"). fromFollow: an end's button began it. Start again and the
// switch keep fromFollow and chain as the first begin recorded them.
export interface LastAct { act: Act | "enroll"; type?: string; rekey?: boolean; withRecoveryKey?: boolean; fromFollow: boolean; chain: Chain | null }
// The chain an act begins in: begun from an end's button, prev; begun from
// the page, a new chain with nextId when the act re-keys (the re-keying
// answer, a replacement), else none.
export function chainFor(prev: Chain | null, fromFollow: boolean, act: Act | "enroll", rekey: boolean, nextId: number): Chain | null;
// The chain after an act's Done: a replacement's newIds join replaced; a
// re-keying password change or turning-on sets changed. Unchanged by any
// other end.
export function chainAfter(ch: Chain | null, c: { kind: string; step: string; rekey?: boolean; newIds?: string[] }): Chain | null;
// The committed facts an act's final state carries (CeremonyState.after,
// 2.5), by which the end's next step is decided, never the page's slots.
export interface AfterFacts { recoveryIds: string[]; entangled: boolean; setupNeeded: boolean }
// The end's next step (ruling 2; one chain, the password first, critique 2
// UX 4): its sentence and the command its button opens, or null. Only at a
// Done of the act LastAct names whose final state carries after, a.chain
// being as chainAfter left it at this Done. A vault left with recovery keys
// alone (after.setupNeeded) is answered first, on every branch: "add" after
// a re-keying removal of a YubiKey or an enrolment begun from an end's
// "add" (a recovery key added from there leaves the vault so), null after
// anything else, the replacement being refused there and the Vault page's
// own line saying to add an unlock method; never "change" or "replace" on
// such a vault (critique 3, M3). Otherwise, only for:
//  - a re-keying removal: the password on -> "change"; else a recovery key
//    left, not every one of which is in the chain's replaced -> "replace";
//  - an enrolment begun from an end's "add": the password on and the chain
//    not changed -> "change"; else a recovery key left, not all in the
//    chain's replaced -> "replace";
//  - a re-keying password change, or turning the password on with a
//    re-key: a recovery key left, not every one of which is in the chain's
//    replaced -> "replace";
//  - a replacement: the password on and the chain not changed -> "change";
//    else null.
// Never after a removal answered "kept", a change or turning-on without a
// re-key, a re-key alone or an export.
export interface FollowUp { text: string; act: "add" | "change" | "replace" }
export function followUp(a: LastAct | null, c: { kind: string; step: string; rekey?: boolean; after?: AfterFacts }): FollowUp | null;
// Why Replace recovery keys… is greyed, or undefined (3.3): needs unlock,
// changesOff, noRecoveryToReplace, or replaceNeedsProtector where every
// method is a recovery key.
export function replaceGreyed(f: { unlocked: boolean; tampered: boolean; recoveryKeys: number; setupNeeded: boolean }): string | undefined;
// What the replacement's dialog reads (3.3 (b); critique 3, M1 and N10):
// opened from an end's follow-up (from set), its count and the switch from
// that end's committed facts and changed from the chain; opened from the
// page, from the store, and changed false, a new chain beginning with it.
// changed leaves out whenOn and howOn.
export function replaceFacts(o: { from?: { after: AfterFacts; changed: boolean }; recoveryKeys: number; entangled: boolean }): { count: number; passwordOn: boolean; changed: boolean };
// The recovery view's note in a dialog, by why a recovery key proves it
// (critique 2, UX 8): without set, the removal's; an enrolment begun with
// the switch, the enrolment's, by the kind it adds (enroll for a YubiKey,
// enrollRecovery for a recovery key); the kinds ceremonyCopy.recoveryNote already
// names (verify, import, records), theirs; any other, a vault of recovery
// keys alone, recoveryNote.only (with recoveryNote.nextMethod for an
// enrolment).
export function recoveryNoteOf(c: { kind: string; without?: string }, a: LastAct | null): string;
// What a replacement's end means for the keys it showed (the outside
// review's second reading, finding 9), one classification for the dialog
// and the Vault page's notice: "replaced" at Done; "unknown" when it failed
// with vault.indeterminate or vault.conflict, from its commit or its
// retirement, after newIds were seen — the keys may be the vault's, so
// nothing says to destroy them; "notAdded" when it failed any other way
// after newIds were seen, which can only be before its commit landed
// (2.4: a landed commit ends Done or Broken); null otherwise.
export type ReplaceResult = "replaced" | "notAdded" | "unknown";
export function replaceResult(c: { kind: string; step: string; error?: string; newIds?: string[] }): ReplaceResult | null;
// The Vault page's notice about keys a replacement showed (3.3): their IDs,
// and whether it is known that they were never added. A "notAdded" or
// "unknown" end raises it; a Done or a new replacement's first event clears
// it; an unknown one is settled by listed, the slots read after the reopen:
// every ID listed clears it, none listed makes it known. Otherwise prev.
export interface ReplaceNotice { ids: string[]; known: boolean }
export function replaceNotice(prev: ReplaceNotice | null, c: { kind: string; step: string; error?: string; newIds?: string[] } | null, listed?: readonly string[]): ReplaceNotice | null;
```

`backupOwed` is KeysPage.svelte:194-199 moved here and extended (`lastExportAt` 0 is never; the
core already reads a stamp ahead of the clock as 0, keys.go:951-962); the Re-key dialog uses it.
`replaceResult` reads only the event; `replaceNotice` is what the store keeps between events,
across a lock and a Broken state, so that the page can say it wherever the user is next (3.3).

Three small components carry the shared markup, so that the dialogs draw it one way:
frontend/src/components/RekeyQuestion.svelte (the fieldset with `data-question`, its legend, two
radios and their second lines; props `legend`, `rekey`, `keep`, a bindable `answer`),
frontend/src/components/BackupChoice.svelte (the fact, the reason, the tick or the no-recovery
line; props `act`, `rekey` (the answer), `removing`, a bindable `ticked`, reading
`store.lastExportAt`, `store.backup`, `store.slots` and `store.now`; `ticked` is set from
`backupTicked` when it mounts and again whenever `rekey` changes) and
frontend/src/components/ReplaceDialog.svelte (3.3 (b); props `onclose`, `beginWith` and
`from`).

The store gains three fields: `replaceNotice` (3.3), kept when the vault leaves Unlocked since an
ID is not secret; `lastAct`, moved from KeysPage so that the follow-up survives the ceremony
dialog's close and reopen; and `chain`, with `chainSeq` for the next chain's id, set through
`chainFor` when an act begins and through `chainAfter` at its Done. `lastAct` and `chain` are
dropped when the vault leaves Unlocked: a lock closes every dialog, so no end's button can
continue a chain past it. Revision 3's `replacedIds`, every key a replacement had made since the
page loaded, is gone: a key made earlier in the same window says nothing about whether it is still
unexposed (finding 6). KeysPage's `beginWith` and the end's buttons pass `fromFollow`, the
follow-up's press true and every other start false; *Start again* and the switch record no new
start, keeping the act's own (critique 3, N9).

### 3.6 (e) The ceremony panel

What the user sees, with one proof:

- A removal, in the dialog titled *Remove an unlock method* (strings.ts:1721, as now): on a vault
  with a YubiKey left to prove with, "Insert a YubiKey that already unlocks this vault" over the
  vault's YubiKeys by name, the one being removed left out on the re-keying answer; the PIN (with
  the vault's password when on) in one form; the touch, the box going green; "Checking that it is
  you…" on the green; then the lead turns to what is being done: "Removing the unlock method and
  re-keying the vault…" (or "Removing the unlock method…"), then, with a backup, "Saving the
  backup…"; then the check and the end: "Unlock method removed and vault re-keyed" (or "Unlock
  method removed"); *Close*. Where the YubiKey removed was the only one, the proof is the recovery
  view (CeremonyPanel.svelte:308-314) with the removal's note (`recoveryNoteOf`, 3.5). On a vault
  with no YubiKey:
  "Type a password that already unlocks this vault" over "It proves it is you.", the derivation on
  the neutral panel, the same leads and end.
- A password change, in the dialog titled **Change the password for every YubiKey** (new): the
  same proof; then the vault's chosen password, twice, with the present label and note
  (CeremonyPanel.svelte:95-98, 103-105); *Continue*; "Changing the password and re-keying the
  vault…" on the neutral panel while the new password is derived and written; the end on the
  neutral panel, a prompt having come between the touch and the end (APP.md:3286-3290): "Password
  changed and vault re-keyed" (or "Password changed").
- Turning the password on: as the change, with "Turning the password on and re-keying the
  vault…" (or "Updating every YubiKey…", which turning it off also shows) and the end "Password
  turned on and vault re-keyed" (or the generic "Done", as now).
- A re-key: as today, with "Re-keying the vault…" after the proof and the end "Vault re-keyed".
- A replacement: 3.3 (d); the keep steps are the recovery key's dialog, not this panel.
- An enrolment proved with a recovery key: 3.4.
- A plain export: "Saving the backup…" after the proof.

**The end** (the "done" view, CeremonyPanel.svelte:342-344, and the touch view's Done, 319-333).
An end that carries any of the items below is drawn on the neutral done view, whatever came
before it (critique 2, UX 7): `dialogViewOf` (lockscreen.ts:461-468) answers "done" for StepDone
when `DialogFacts` says the end has more (`endsWithMore`, from `retireError`, `backupError`, a
replacement's `newIds`, a follow-up or the switch's next-step line). The touch view's Done holds
132 px of rings and a hero title (`.touch-lead`, app.css:1172), so "Unlock method removed and vault
re-keyed" takes two lines there and the items would pass the panel's height; and an attention bar
on the touch's green would read as success and failure at once, the green being only ever a
touch's (lockscreen.ts:454-460). An end with nothing more keeps the green as now. Under the title,
at the view's full width and read from the left, in this order, each only when it applies:

1. The replacement's receipt and its backup line (3.3 (d) 5).
2. `retireError`: an attention bar, `ceremonyCopy.retireNotFinished(lead, why, backupToo)`: its
   lead, then {why}, then "Until Enfold has cleared it, changes to the unlock methods, re-keying
   the vault, backups, and creating, importing, re-keying, forgetting, restoring or deleting
   archives wait; Enfold tries again at each of them and at the next unlock. You can still open
   your archives and change the files inside them." (`retireHeld`, the list of 2.4's table and no
   other, which the barrier's refusal says too; R-A: the content of an archive is not held;
   critique 3, M2), and, when the act's backup was skipped for it (`backupError`
   `vault.retire_owed`), "The backup you asked for waits too." The lead (`retireLeadOf`, 3.7;
   critique 3, finding 6), by what is left:
   - a slot region copy or the losing superblock still owed: "The vault file still holds its state
     from before this change: a copy of it taken now {what}." — {what} by the act (critique 2,
     nit 18): "still opens with the unlock method removed", "still opens with a YubiKey and the old
     password", "still opens with a YubiKey alone" (turning the password on), "still opens with
     the replaced recovery keys" ("…recovery key" for one), "still holds the vault's old key" (a
     re-key); after an enrolment or turning the password off, which withdrew nothing, the lead
     ends at "change" (revision 4 gave turning it off the turn-on's {what}, false there, since the
     state before asked for the password);
   - only earlier registry ciphertext (`retireRemnant`): "The vault file still holds earlier
     versions of its list of archives, which the vault's old key reads." after an act that
     re-keyed, and "The vault file still holds earlier versions of its list of archives." after one
     that did not, since those are under the key the vault still has and reach nothing the method
     withdrawn could. Neither says a copy opens with the method withdrawn: the record is gone from
     both region copies by then.
3. `backupError`: an attention bar, "The backup was not saved. {why} Export one from Backups and
   copies." (`ceremonyCopy.backupNotSaved`); when it is `vault.retire_owed`, no bar of its own: the
   retirement's bar says it (item 2; critique 3, M7), and a replacement's line is then the "not
   asked for" one (3.3 (d) 5). It never stands without `retireError`: `backUpAfter` skips for it
   only after a retirement that did not land (2.3).
4. The follow-up (`followUp`, 3.5), decided from the committed facts the final state carries
   (`after`, 2.5) and the chain (3.5), never from the page's slot list, which may still be the
   one read before the act ended: KeysPage reads the slots when `Keys.*` returns and when the
   dialog closes (KeysPage.svelte:77-87, 328-335), and the store only at a change of state
   (state.svelte.ts:1006, 1036), not at an act's Done while Unlocked (the outside review's second
   reading, finding 7). An info line with its icon and, in it, a small standard button that opens
   the next act (the cure is a command, so a button, KeysPage.svelte:441). One chain, the password
   first (critique 2, UX 4; 3.3 (i)):
   - after a re-keying removal of a YubiKey that left the vault with recovery keys alone: "Add the
     new YubiKey now; then, if an earlier copy of the vault file may be out there, replace the
     recovery keys." (with the password on, "…change the password and replace the recovery keys.")
     and the button *Add an unlock method…*; the enrolment it begins carries `chain`, so its own
     end goes on with the next step below (critique 2, F10 and UX 2: the replacement is refused
     there); an enrolment begun from it that leaves the vault with recovery keys alone still, a
     recovery key added, ends on the same line and button (critique 3, M3);
   - after a re-keying removal, or such an enrolment, with the password on: "If an earlier copy of
     the vault file may be out there, change the password, then replace the recovery keys." (after
     a recovery key's removal, "If an earlier copy or backup may be out there, change the
     password, then replace the other recovery keys.") and the button *Change…*;
   - after a re-keying removal, or such an enrolment, with the password off and a recovery key
     left: "If an earlier copy of the vault file may be out there, replace the recovery keys too."
     (after a recovery key's removal, "If an earlier copy or backup may be out there, replace the
     other recovery keys too.") and the button *Replace recovery keys…*;
   - after a re-keying password change, or turning the password on with a re-key (critique 2,
     UX 12: the exposure is the same), with a recovery key left that the chain's replacement did
     not make: "If an earlier copy of the vault file may be out there, replace the recovery keys
     too." and *Replace recovery keys…*;
   - after a replacement, with the password on and no re-keying change of it earlier in the chain:
     "If an earlier copy of the vault file may be out there, change the password too: the vault's
     own record of it was readable the same way." (`followUp.changeAfterReplace`) and *Change…*;
   - nothing after a replacement otherwise, after a removal answered "kept", where no recovery key
     is left, or after a removal of a recovery key from a vault of recovery keys alone, where the
     replacement is refused and the Vault page's own line already says to add an unlock method;
     and nothing for an end whose final state carries no `after` (an older core, the mock without
     it), which fails toward saying less.
   The panel takes it as a prop, `follow?: { text: string; label: string; onpress: () => void }`,
   from KeysPage, which reads the store's `lastAct` and `chain` and the final state's `after`; the
   press closes the ceremony dialog (`closeCeremony`) and opens the next act's dialog — the Add
   dialog, the password's *Change…* dialog, or the replacement's with `from`, the final state's
   `after` and the chain's `changed` (3.3 (b)) — and that act begins with `fromFollow`, in the
   same chain. While the end carries `retireError`, the line ends with " Enfold first tries again
   to finish clearing the vault file." (`followUp.retryFirst`): the next act's begin is the retry
   (2.4), which the retirement's bar above says waits, so the button stays and its press is said
   for what it is (critique 3, M7).
5. After an enrolment of a YubiKey begun with the switch (`lastAct.withRecoveryKey`; not any
   enrolment proved with a recovery key, since a vault of recovery keys alone proves every
   enrolment so; a recovery key's enrolment ends in the recovery key's dialog, 3.4): the line
   "Now remove the lost YubiKey: Remove… on its card asks why, and re-keys the vault when it was
   lost." in the second ink (`ceremonyCopy.afterRecoveryProof`).

The end's height on the neutral view, by estimate from app.css and not measured: the 52 px disc
(app.css:1169), the view's 12 px gap and a one-line title come to about 90 px; a bar is 8 px of
padding above and below its 20 px lines (app.css:674-677), so 58 px for two lines and 78 for
three; a receipt row is 20 px; the follow-up's info line with its button about 70, and 90 with the
retry sentence. A removal's end with its follow-up is then about 175 px; a replacement of two keys
with its backup written about 210. The retirement's bar is the longest item: its lead, its reason
and the list of what waits come to about eight lines at the view's width, nine with the skipped
backup's sentence, so about 176 to 196 px. A removal whose retirement failed, with its backup
skipped, comes to about 300, inside the panel's 356 px (CeremonyPanel.svelte:395-405), now that
the skipped backup is a sentence of that bar rather than a bar of its own (critique 3, M7; it was
about 345); with its follow-up as well about 400, past it, and a replacement of two keys whose
retirement failed, with its receipt, the bar and the "not asked for" line, about 450, and 540 with
its follow-up. The panel's height is a `min-height` (405), so such an end grows rather than clips,
once, at the act's last view; it is the rare end, and its words are the ones that matter then.

**The failed view** (CeremonyPanel.svelte:345-355) of kinds remove, password, entangle, rotate and
replace adds the caption "Nothing was changed." under its danger bar, except for
`vault.indeterminate` and `vault.conflict`, whose own words say a save could not be confirmed or
another writer changed the file (true by 2.2.3's table: a failure before the change's flip leaves
the old state whole and no backup is written before the change). A replacement adds, by
`replaceResult` (3.5): for "notAdded", `ceremonyCopy.keysNeverAdded` — "The recovery keys shown
were never added: destroy what you kept of them."; for "unknown", whether the code is
`vault.indeterminate` or `vault.conflict` and whether it came from the replacement's commit or its
retirement, `ceremonyCopy.keysWorkIfListed`: "The new recovery keys work only if the Vault page
lists their IDs after you reopen it: keep every sheet until you have looked." Each is in the
singular for one key, "keep both sheets" in the second (`keysNeverAddedOne`,
`keysWorkIfListedOne`; critique 3, N1). Neither is chosen after the commit has landed: an act whose
commit landed ends Done, save for the two codes that enter Broken (2.4). Never the first for
a conflict: revision 3 chose by `vault.indeterminate` alone, so a conflict from the retiring
commit, after the replacement had committed, told the user to destroy keys that were the vault's
(finding 9).

**The confirm view**, at `StepConfirm` (`viewOf` gains `"confirm"` for that step; lockscreen.ts):
the lead `replaceCopy.ready`, the label · value list with one row per key, the key's label and
`replaceCopy.pair(newId, oldId)`, and the caption `replaceCopy.readyLine`; it asks (`asks`
includes it while `c.promptId` is set), so `dialogFoot` gives *Continue* and *Cancel*, and the
panel labels *Continue* `primaryOf("replace", true, false, count)` there; its form's submit sends
`submitSecret("confirm", c.promptId, "")`.

**The switch** of 3.4: a prop `onrecovery?: () => void`, which KeysPage passes for every
enrolment; the panel draws the switch, and gives `dialogFoot` its `recovery` flag, only while
`offersRecoverySwitch(c, store.lastAct, store.slots, second)` holds (lib/lockscreen.ts, below;
critique 3, M5), so that APP.md's one exception at *No match* is one tested rule rather than a
condition spread over two components. With it, the insert view draws
`lockCopy.useRecoveryInstead` as a subtle small button under the lead; and at
*No match* the edge view's caption is the step's own body (`stepText(c.step).body`) rather than
`ceremonyCopy.noMatchWithoutRecovery`, and `dialogFoot` (lockscreen.ts:437-440), given `recovery:
true`, answers `["recovery", "cancel"]` there: the accent `lockCopy.useRecoveryKey`, grown, then
*Cancel*, the lock screen's own row (Lock.svelte:796-809). `FootButton` gains "recovery"; every
other view's foot is unchanged.

Code changes: the end's lookup, at CeremonyPanel.svelte:189 and 344, becomes `doneTitleOf(c)`;
`derivingLead` (77) becomes `phaseLeadOf(c) ?? (second ? lockCopy.derivingLead.adding :
ceremonyCopy.checking)`; the choose label and note test `choosesVaultPassword(c.kind)` instead of
`c.kind === "entangle"` (96, 104); `proves` (130) becomes `provesFirst(c.kind)`;
`hardwareLabels` (131) leaves out `s.recipientId === c.without`; the failed view tests
`nothingChanged(c)`. The predicates go into frontend/src/lib/lockscreen.ts, so that the lists are
tested:

```ts
// The acts that prove it is you with an unlock method the vault already has,
// so that their wait names the vault's YubiKeys (APP.md §13).
export function provesFirst(kind: string): boolean; // enroll, remove, rotate, export, reveal, entangle, password, records, replace
// The acts whose chosen password is the vault's, asked of every YubiKey.
export function choosesVaultPassword(kind: string): boolean; // entangle, password
// A failure of these acts changed nothing, unless the save could not be
// confirmed (Broken says so in its own words).
export function nothingChanged(c: { kind: string; error?: string }): boolean; // remove, password, entangle, rotate, replace
// The recovery-key switch of 3.4 is offered (critique 3, M5): an enrolment
// (kind enroll) of a YubiKey or a recovery key (a.type "token" or
// "recovery"), begun without the switch (not a.withRecoveryKey), not in its
// second half, on a vault whose slots hold a YubiKey and a recovery key.
// APP.md §6's one exception at No match, as a predicate.
export function offersRecoverySwitch(c: { kind: string }, a: { act: string; type?: string; withRecoveryKey?: boolean } | null, slots: readonly { type: string }[], second: boolean): boolean;
```

The ceremony dialog's titles for kinds "password" and "replace": `vaultCopy.ceremonyTitle`
(strings.ts:1719-1726) gains them; without them a new kind would be titled "Export a backup"
(KeysPage.svelte:639). KeysPage's exclusion list (37) needs nothing. While the ceremony is at
`StepKeepKey`, KeysPage mounts `RecoveryReveal` in keep mode instead of this dialog (3.3).

### 3.7 (f) The copy

In frontend/src/lib/strings.ts, `ceremonyCopy` (from 112; `doneTitle` at 173-178, `recoveryNote`
at 180-184):

```ts
  doneTitle: { enroll: "Unlock method added", remove: "Unlock method removed", rotate: "Vault re-keyed", export: "Backup saved", password: "Password changed" },
  // An act that re-keyed the vault in the same commit says both (ruled
  // 2026-10-27, CeremonyState.rekey).
  doneRekeyed: {
    remove: "Unlock method removed and vault re-keyed",
    password: "Password changed and vault re-keyed",
    entangle: "Password turned on and vault re-keyed",
    replace: "Recovery keys replaced and vault re-keyed",
    replaceOne: "Recovery key replaced and vault re-keyed",
  } as Record<string, string>,
  // What is being done once the proof has passed (CeremonyState.phase).
  phaseLead: {
    remove: "Removing the unlock method…",
    removeRekey: "Removing the unlock method and re-keying the vault…",
    password: "Changing the password…",
    passwordRekey: "Changing the password and re-keying the vault…",
    entangle: "Updating every YubiKey…",
    entangleRekey: "Turning the password on and re-keying the vault…",
    rotate: "Re-keying the vault…",
    replaceRekey: "Replacing the recovery keys and re-keying the vault…",
    replaceRekeyOne: "Replacing the recovery key and re-keying the vault…",
    backup: "Saving the backup…",
  } as Record<string, string>,
  nothingChanged: "Nothing was changed.",
  backupNotSaved: (why: string): string => `The backup was not saved. ${why} Export one from Backups and copies.`,
  // R41's retirement did not land; the change stands, and what writes a
  // record or a secret waits for it while archive content does not (ruled
  // 2026-10-27, R-A). lead is retireLeadOf's; backupToo, the act's backup
  // skipped for it (BackupError vault.retire_owed), which has no bar of its
  // own (critique 3, M7). The list is retireHeld, the barrier's own.
  retireNotFinished: (lead: string, why: string, backupToo: boolean): string =>
    `${lead} ${why} Until Enfold has cleared it, ${retireHeld}${backupToo ? " The backup you asked for waits too." : ""}`,
  // The bar's first sentence, by what is left (critique 3, finding 6).
  retireLead: {
    state: (what: string): string => `The vault file still holds its state from before this change${what ? `: a copy of it taken now ${what}` : ""}.`,
    // Only earlier registry ciphertext is left (RetireRemnant): after a
    // re-key under the old key; otherwise under the key the vault has,
    // which reaches nothing the method withdrawn could.
    remnantOldKey: "The vault file still holds earlier versions of its list of archives, which the vault's old key reads.",
    remnant: "The vault file still holds earlier versions of its list of archives.",
  },
  // What a copy of the file taken now still opens with, by the act; none
  // after an enrolment or turning the password off, which withdrew nothing.
  retireWhat: {
    remove: "still opens with the unlock method removed",
    password: "still opens with a YubiKey and the old password",
    entangle: "still opens with a YubiKey alone",
    replace: "still opens with the replaced recovery keys",
    replaceOne: "still opens with the replaced recovery key",
    rotate: "still holds the vault's old key",
  } as Record<string, string>,
  keysNeverAdded: "The recovery keys shown were never added: destroy what you kept of them.",
  keysNeverAddedOne: "The recovery key shown was never added: destroy what you kept of it.",
  keysWorkIfListed: "The new recovery keys work only if the Vault page lists their IDs after you reopen it: keep every sheet until you have looked.",
  keysWorkIfListedOne: "The new recovery key works only if the Vault page lists its ID after you reopen it: keep both sheets until you have looked.",
  // The end's next step (ruled 2026-10-27; one chain, the password first):
  // lib/rekey.ts followUp picks one; the button names its command.
  followUp: {
    addFirst: "Add the new YubiKey now; then, if an earlier copy of the vault file may be out there, replace the recovery keys.",
    addFirstOn: "Add the new YubiKey now; then, if an earlier copy of the vault file may be out there, change the password and replace the recovery keys.",
    changeFirst: "If an earlier copy of the vault file may be out there, change the password, then replace the recovery keys.",
    changeFirstRecovery: "If an earlier copy or backup may be out there, change the password, then replace the other recovery keys.",
    replace: "If an earlier copy of the vault file may be out there, replace the recovery keys too.",
    replaceRecovery: "If an earlier copy or backup may be out there, replace the other recovery keys too.",
    // After a replacement, the password on and not yet changed in the chain.
    changeAfterReplace: "If an earlier copy of the vault file may be out there, change the password too: the vault's own record of it was readable the same way.",
    // Added while the end carries retireError: the next act's begin is the
    // retry (critique 3, M7).
    retryFirst: " Enfold first tries again to finish clearing the vault file.",
  },
  // After an enrolment begun with the switch (3.4), never after any other.
  afterRecoveryProof: "Now remove the lost YubiKey: Remove… on its card asks why, and re-keys the vault when it was lost.",
  // recoveryNote gains, chosen by cause through lib/rekey.ts recoveryNoteOf:
  //   enroll: "Your recovery key proves it is you, since no YubiKey of this vault is at hand. The new YubiKey comes next.",
  //   enrollRecovery: "Your recovery key proves it is you, since no YubiKey of this vault is at hand. The new recovery key comes next.",
  //   remove: "One of this vault's recovery keys proves it is you, since the YubiKey being removed is not asked for.",
  //   only: "This vault has only its recovery keys, so one of them proves it is you.",
  //   nextMethod: " The new unlock method comes next.",

// The end of a dialog's ceremony, what is now true (APP.md §6, Controls).
export function doneTitleOf(c: { kind: string; rekey?: boolean; keepCount?: number }): string | undefined {
  if (c.kind === "replace") return (c.keepCount ?? 0) === 1 ? ceremonyCopy.doneRekeyed.replaceOne : ceremonyCopy.doneRekeyed.replace;
  return (c.rekey ? ceremonyCopy.doneRekeyed[c.kind] : undefined) ?? ceremonyCopy.doneTitle[c.kind];
}
// The lead after the proof, by phase (APP.md §6, Controls); undefined during the proof.
export function phaseLeadOf(c: { kind: string; rekey?: boolean; phase?: string; keepCount?: number }): string | undefined {
  if (c.phase === "backup") return ceremonyCopy.phaseLead.backup;
  if (c.phase !== "commit") return undefined;
  if (c.kind === "replace" && c.keepCount === 1) return ceremonyCopy.phaseLead.replaceRekeyOne;
  return ceremonyCopy.phaseLead[c.rekey ? `${c.kind}Rekey` : c.kind];
}
// The retirement bar's first sentence (3.6 item 2; critique 3, finding 6):
// earlier registry ciphertext alone, worded by whether the act re-keyed;
// else the state before, with what a copy taken now opens with, none after
// an enrolment or turning the password off (after.entangled false).
export function retireLeadOf(c: { kind: string; rekey?: boolean; keepCount?: number; retireRemnant?: boolean; after?: { entangled: boolean } }): string {
  if (c.retireRemnant) return c.rekey || c.kind === "rotate" ? ceremonyCopy.retireLead.remnantOldKey : ceremonyCopy.retireLead.remnant;
  if (c.kind === "enroll" || (c.kind === "entangle" && c.after?.entangled === false)) return ceremonyCopy.retireLead.state("");
  const key = c.kind === "replace" && c.keepCount === 1 ? "replaceOne" : c.kind;
  return ceremonyCopy.retireLead.state(ceremonyCopy.retireWhat[key] ?? "");
}
```

`retireHeld` is a module constant declared before `ceremonyCopy` and `codeCopy`, which both read
it:

```ts
// What waits while a retirement is owed: one list for the act's bar and the
// barrier's refusal, 2.4's table by its on-screen names, then the content
// that does not wait (ruled 2026-10-27, R-A; critique 3, M2).
export const retireHeld =
  "changes to the unlock methods, re-keying the vault, backups, and creating, importing, re-keying, forgetting, restoring or deleting archives wait; Enfold tries again at each of them and at the next unlock. You can still open your archives and change the files inside them.";
```

In `dialogCopy` (1471-1592):

```ts
  // The question at a removal, a password change and turning the password
  // on (DESIGN §5; ruled 2026-10-27): the fact, not the technical choice.
  rekeyQuestion: {
    hardware: "Why are you removing this YubiKey?",
    recovery: "Why are you removing this recovery key?",
    password: "Why are you removing this password?",
    change: "Why are you changing the password?",
    on: "Could the vault file have been copied before now?",
    lost: "Lost, stolen, or I am not sure",
    kept: "I still have it and it was never out of my control",
    known: "Someone else may know it, or I am not sure",
    onlyMe: "Only I have ever known it",
    copied: "Yes, or I am not sure",
    notCopied: "No, it was never copied",
    rekeys: "The vault is re-keyed, as recommended.",
    keeps: "The vault keeps its key.",
  },
  // The limit of a re-key, said plainly (DESIGN §5), what the recovery keys'
  // escrow leaves open (FORMAT R38), and the act that closes it (ruled
  // 2026-10-27): by act.
  // The second sentence of each is drawn only while the vault has a
  // recovery key besides any being removed (3.1, item 5); none ends on the
  // command's "…" (critique 2, nit 16).
  rekeyLimit: {
    future: "Re-keying protects the future, not the past: whatever someone could open until now, they may already have copied.",
    method: (noun: string): string =>
      `If an earlier copy of the vault file may be out there, whoever opened it with this ${noun} could also read the recovery keys, which still open this vault: replace them afterwards with Replace recovery keys… on this page.`,
    methodOn:
      "If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the recovery keys and the vault's own record of the password: change the password afterwards, then replace the recovery keys with Replace recovery keys… on this page.",
    methodOnNoRecovery:
      "If an earlier copy of the vault file may be out there, whoever opened it with this YubiKey could also read the vault's own record of the password: change the password afterwards.",
    recovery:
      "Whoever has this recovery key can open an earlier copy or backup of the vault and read the other recovery keys there: if one may be out there, use Replace recovery keys… on this page instead, which removes this one too. Check the unlock methods as well, and remove any you did not add.",
    recoveryOn: " Then change the password.",
    change:
      "If an earlier copy of the vault file may be out there, whoever opened it with a YubiKey and the old password could also read the recovery keys: replace them afterwards with Replace recovery keys… on this page.",
    on: "Whoever opens an earlier copy of the vault file with a YubiKey alone can also read the recovery keys: if such a copy may exist, replace them afterwards with Replace recovery keys… on this page.",
  },
  // The backup an act offers to take after its change (APP.md §13).
  backupAfter: {
    tick: "Export a backup afterwards",
    whyRekey: "A backup made before the re-key is at the vault's old key, and restoring it would bring that key back; one made afterwards is not.",
    whyRecovery: (label: string): string => `Backups made while ${label} was an unlock method hold it and open with it; one made afterwards does not.`,
    whyReplace: "Backups made before now open only with the old recovery keys; one made afterwards holds the new ones.",
    whyReplaceOne: "Backups made before now open only with the old recovery key; one made afterwards holds the new one.",
    none: "No backup of this vault can be made without a recovery key.",
  },
  // The primaries, by act, answer and tick (lib/rekey.ts primaryOf).
  primaries: {
    remove: ["Remove unlock method", "Remove and re-key", "Remove, then back up", "Remove, re-key and back up"],
    password: ["Change password", "Change and re-key", "Change, then back up", "Change, re-key and back up"],
    on: ["Turn on password", "Turn on and re-key", "Turn on, then back up", "Turn on, re-key and back up"],
    rotate: ["Re-key vault", "Re-key vault", "Re-key, then back up", "Re-key, then back up"],
    replace: ["Replace recovery keys", "Replace recovery keys", "Replace, then back up", "Replace, then back up"],
  } as Record<string, [string, string, string, string]>,
  replaceOne: "Replace recovery key",
  removeUnlockMethod: {
    title: (label: string, added?: string): string => (added ? `Remove ${label} (added ${added})?` : `Remove ${label}?`),
    // A backup holds the recovery keys alone (FORMAT R28): only a recovery
    // key's removal leaves backups that still open with it.
    body: (label: string, type: string): string =>
      type === "recovery"
        ? `${label} stops unlocking this vault now. Copies and backups of the vault made earlier still open with it.`
        : `${label} stops unlocking this vault now. An earlier copy of the vault file still opens with it; a backup never held it.`,
    retired: (n: number, label: string): string =>
      n === 1
        ? `Enfold keeps 1 replaced vault file in its folder; if it had ${label}, it still opens with it until you delete it.`
        : `Enfold keeps ${n} replaced vault files in its folder; those that had ${label} still open with it until you delete them.`,
    lastProtector: "After this the vault has only its recovery keys: add the new YubiKey here now, or after unlocking with a recovery key.",
    how: (label: string, type: string): string => /* the two texts of 3.1 item 7 */,
  },
  // Replace recovery keys… (ruled 2026-10-27): one ceremony, one commit.
  replaceRecoveryKeys: {
    title: (n: number): string => (n === 1 ? "Replace the recovery key?" : `Replace the ${n} recovery keys?`),
    body: (n: number): string =>
      n === 1
        ? "Enfold makes a new recovery key for you to keep, and the old one stops unlocking this vault. Copies and backups of the vault made before now still open with the old one."
        : `Enfold makes ${n} new recovery keys for you to keep, one for each, and the old ones stop unlocking this vault. Copies and backups of the vault made before now still open with the old ones.`,
    when: (n: number): string =>
      n === 1
        ? "Replace it when an earlier copy of the vault file, or a backup, may have been opened by someone else: whoever did could read the recovery key in it, which keeps opening this vault until it is replaced. If that copy was opened with a YubiKey or a password, remove that unlock method first, and remove any unlock method you did not add."
        : "Replace them when an earlier copy of the vault file, or a backup, may have been opened by someone else: whoever did could read every recovery key in it, and those keep opening this vault until they are replaced. If that copy was opened with a YubiKey or a password, remove that unlock method first, and remove any unlock method you did not add.",
    // With the password on, unless opened from a password change's end.
    whenOn: " Change the password too: the vault's own record of it was readable the same way.",
    ready: (minutes: number): string =>
      `Have somewhere to keep each new recovery key before you start: print it, save it as a text file, or write it down. Nothing changes until you have kept every one and confirmed. Enfold locks after ${plural(minutes, "minute")} without input, which ends the replacement with nothing changed: printing or saving is quicker than writing 48 digits by hand.`,
    how: "Enfold makes the new keys first and shows each one in turn. When you confirm, one step adds them, removes every old recovery key and re-keys the vault, so the copy of each new recovery key the vault keeps is never readable with an old one. An earlier copy or backup then reaches nothing in this vault through its recovery keys; an archive that existed then stays open to it, files added later included, until that archive is re-keyed with Re-key archive… on its page. Each new recovery key takes the name of the one it replaces, so you know where it goes.",
    howOn: " Such a copy also held the vault's record of the password, which only changing the password replaces.",
  },
```

`passwordOn.body` and `.how`, `passwordChange.body` and `.how`, and `rekeyVault.body` take the
words of 3.2 — `passwordChange.how` beginning with the disclosure said truly, "This dialog has
no field for the old password: whoever reaches this page has already opened the vault, and could
add an unlock method of their own. Proving it is you with a YubiKey still asks for the current
password with its PIN." (the outside review's second reading, finding 11; it said "The old
password is not asked for", strings.ts:1579); the two comments that still promise it,
strings.ts:1567-1568 ("the old password is never asked") and keys.go:64-65 ("The old password is
not asked in either direction"), say instead that no field asks for the old password, though a
YubiKey's proof asks the current one with its PIN (keys.go:106-107, "the old password is never a
field", is already true; critique 3, N5); `rekeyVault.backupFirst` and `.primaryWithBackup`
go (the shared block and `primaries` replace them; `.lastBackup`, `.noBackup` and `.backupWhy`
stay and are what the block reads); `removeUnlockMethod.primary` goes into `primaries`. A new
`replaceCopy` holds the ceremony's own words, beside `revealCopy` (2116-2145):

```ts
// Replace recovery keys… inside its ceremony (APP.md §13, ruled 2026-10-27).
export const replaceCopy = {
  ready: "Ready to replace",
  newKeys: "New",
  replacedKeys: "Replaced",
  readyLine: "The replaced keys stop unlocking this vault, and every new one works from now on. Check that you can read each new recovery key before you go on.",
  readyLineOne: "The replaced key stops unlocking this vault, and the new one works from now on. Check that you can read the new recovery key before you go on.",
  // One row per key at the confirmation and in the receipt, the key's
  // label as the row's label (critique 2, nit 17).
  pair: (newId: string, oldId: string): string => `${newId} replaces ${oldId}`,
  // The end, by what the backup did (critique 2, UX 5: the earlier
  // backups are said, not ordered destroyed).
  backedUp: "Destroy the replaced keys' sheets and files once the new backup opens (Test that a backup opens…). The backups made before still open with the replaced keys, and this vault can still import archives from them.",
  noBackup: "No backup holds the new keys yet, and every earlier one opens only with the replaced keys. Export one from Backups and copies, and destroy the replaced keys' sheets and files once it opens.",
  afterFailedBackup: "Destroy the replaced keys' sheets and files once a new backup opens.",
  // The same for one key (critique 3, N1: a count of one is singular
  // everywhere, DECISIONS 2026-09-09); chosen by keepCount.
  backedUpOne: "Destroy the replaced key's sheet and file once the new backup opens (Test that a backup opens…). The backups made before still open with the replaced key, and this vault can still import archives from them.",
  noBackupOne: "No backup holds the new key yet, and every earlier one opens only with the replaced key. Export one from Backups and copies, and destroy the replaced key's sheet and file once it opens.",
  afterFailedBackupOne: "Destroy the replaced key's sheet and file once a new backup opens.",
  notAdded: (ids: string[]): string =>
    ids.length === 1
      ? `Nothing was replaced. Recovery key ${ids[0]} was shown but never added: destroy what you kept of it.`
      : `Nothing was replaced. Recovery keys ${listJoin(ids)} were shown but never added: destroy what you kept of them.`,
  // A replacement whose outcome is not known (vault.indeterminate or
  // vault.conflict): never "destroy" (the outside review's finding 9), and
  // words true of both causes (critique 3, N2).
  unknown: (ids: string[]): string =>
    ids.length === 1
      ? `Recovery key ${ids[0]} was shown, and Enfold could not tell whether it replaced the old one. Reopen the vault; the Vault page then lists the recovery keys it has. Keep both sheets until then.`
      : `Recovery keys ${listJoin(ids)} were shown, and Enfold could not tell whether they replaced the old ones. Reopen the vault; the Vault page then lists the recovery keys it has. Keep every sheet until then.`,
};
```

`revealCopy` (2116-2145) gains the keep mode's words:

```ts
  // A replacement's new key, shown before it is the vault's (3.3).
  titleKeep: (i: number, n: number): string => (n === 1 ? "Your new recovery key" : `New recovery key ${i} of ${n}`),
  replaces: (label: string, id: string): string => `Replaces ${label} · ${id}.`,
  keepOpens: "Once you confirm, this recovery key opens the vault file by itself, and backups made from then on. Keep it from others.",
  keepDigitsLine: (n: number): string => `${n} digits. Nothing changes until every new recovery key is kept and you confirm.`,
  // No second confirmation in keep mode either (ruled 2026-09-09, held for
  // the replacement on 2026-10-27): the act's own confirmation is the one.
  // Keep mode, like every showing, is not dismissed by Escape or the
  // backdrop: Cancel is its one way out (critique 3, N7).
  // A key that cannot be shown before the commit (critique 2, UX 9).
  keepGone: "This recovery key could not be shown here. Nothing has changed: cancel, and start the replacement again.",
  next: "Next recovery key",
  continue: "Continue",
  // A recovery key added with the switch (3.4): no YubiKey or password is
  // at hand to show it again with (critique 3, M6).
  digitsLineLater: (n: number): string => `${n} digits. They can be shown again from the Vault page only after you add a YubiKey or a password and prove it.`,
```

`listJoin`, used by `replaceCopy.notAdded`, is the list strings.ts already builds inside
`groupsMistyped` (137-140: "a, b and c"), factored out.

`vaultCopy` (1605-1726) gains `ceremonyTitle.password` "Change the password for every YubiKey",
`ceremonyTitle.replace` "Replace the recovery keys" (and `replaceOne` "Replace the recovery key"),
`backupBeforeRekey: "It was made before the vault was re-keyed."`, `backupKeysReplaced: "It opens
only with recovery keys this vault no longer has."`, `noRecoveryToReplace: "This vault has no
recovery key to replace."`, `replaceNeedsProtector: "Add a YubiKey or a password first: a new
recovery key can be shown again only through one."` and `ruleYubiKey: "To remove this YubiKey, add a
new one or a recovery key first: the vault always keeps at least 2 unlock methods."` (critique 3,
M6); its `setupLine` (1611) becomes a function of the recovery keys' count, the present words for
one and "This vault has only its recovery keys. Add a YubiKey or a password; a recovery key is asked
for first." for two or more (critique 2, UX 2). `commandCopy` (1170) gains `replaceRecoveryKeys:
"Replace recovery keys…"`, a command and so in the verb table's comment (33-44). `importCopy`
(1968-1979) gains `beforeRekey: "It was made before this vault was re-keyed: unlock methods removed
since may still open it, and whatever is added to it later."` and `replacedOwes: "It also still
holds its state from before its last change, so it opens with any unlock method that change removed
too. Delete it once the new vault opens."`, which ImportDialog draws inside its confirmation bar,
after the sentence about the replaced vault's own unlock methods (ImportDialog.svelte:185-190),
while `VaultStatus.RetireOwed` (2.4; critique 3, finding 2). `codeCopy` gains
`[Code.CodeRetireOwed]`, "Enfold has not finished clearing the vault file's earlier state. Until it
has, changes to the unlock methods, re-keying the vault, backups, and creating, importing,
re-keying, forgetting, restoring or deleting archives wait; Enfold tries again at each of them and
at the next unlock. You can still open your archives and change the files inside them." (built from
`retireHeld`): one cause true wherever the owed state came from — a change, a re-key alone, a legacy
file's first unlock here, earlier registry ciphertext alone — and the list of every held act,
deleting and restoring an archive and re-keying the vault among them, which the Delete dialog draws
in its danger bar and the merge in its error notice (DeleteArchiveDialog.svelte:252;
MergeDialog.svelte:55-56; critique 3, M2: it said "The last change to the vault's unlock methods is
not finished", false for all but the first, left those three out, and said the files inside the
archives "still open and change", as if by themselves). It reads right both as the standing notice,
in the attention tier (lib/notices.ts:31-35: every unclassified code is attention, and this one is:
a guarantee is weaker than it should be, APP.md:2669-2674), and as the barrier's refusal of one of
those changes (2.4); and `[Code.CodeSetupPasswordOn]: "This vault's password is on, so its setup
cannot be finished here. Unlock with a recovery key, then add a YubiKey on the Vault page."`, which
the lock screen also uses as the greyed *Finish setting up…*'s title (3.8). One word per thing: the
block reuses `rekeyVault.lastBackup`, `.noBackup` and `.backupWhy`.

The backups card (lib/backup.ts:31-77): `backupKind` answers "behind" also when `b.vaultRekeyed`
or `b.keysReplaced`; its `rest` is then `vaultCopy.backupKeysReplaced` when `keysReplaced`, else
`vaultCopy.backupBeforeRekey`, which stand for the counts (the reason to export again whatever was
created since), and *Export backup…* takes the accent through `suggestExport` as for every
"behind" (lib/backup.ts:58).

The import dialog (frontend/src/components/ImportDialog.svelte:171-176): under the replace
sentence, `importCopy.beforeRekey` when `info.rekeyed` (a copy from before a re-key brings the
retired key back).

The status bar shows the backup's done line when an act's dialog closes on a Done that wrote one
(2.5); it shows nothing else for these acts. The store's `exportEnded`
(frontend/src/lib/state.svelte.ts:1100-1102) reads the backups card again (`refreshLastExport`,
which also reads `BackupStatus`, 1246-1253) at the Done of every act of kind export, remove,
password, entangle, rotate and replace: a re-key moves `vaultRekeyed`, a replacement
`keysReplaced`, and a backup the date. Beside it, at the Done of kinds remove, password, entangle,
rotate, enroll and replace, `actEnded(c)` reads the slots and the switch again
(`refreshSlots`, `refreshEntangled`), and `refreshSlots` gains the read counter `backupRead`
already gives the backups card (`slotsRead`, as 1261-1268), so that a reply older than a later
read is never believed (finding 7); it also applies `chainAfter` and, for kind "replace",
`replaceNotice` (lib/rekey.ts), feeding the slots the read returns to an unknown notice to settle
it (3.3).

No pending label: each primary closes its dialog at once; the Save dialog, when ticked, is the
system's own modal; and `Keys.*` returns as soon as the ceremony has begun (keys.go:217-232), the
ceremony dialog then standing from the first event (KeysPage.svelte:636-642). Nothing waits on the
control that started it (frontend/src/lib/pending.ts:1-8). The panel's phase leads are the
progress.

### 3.8 (g) What is greyed, and why

- *Remove…* on a card: unchanged, greyed while locked ("needs unlock"), while Tampered
  (`vaultCopy.changesOff`), and where `Removable` is false — the reason `vaultCopy.ruleYubiKey` on
  a YubiKey's card, `vaultCopy.rule` on the others (KeysPage.svelte:62, 104-108, 413).
- *Replace recovery keys…*: 3.3 (a), by `replaceGreyed`: "needs unlock", `vaultCopy.changesOff`,
  `vaultCopy.noRecoveryToReplace`, or `vaultCopy.replaceNeedsProtector` where every method is a
  recovery key.
- *Change…*, *Turn off…*, *Turn on…*, *Re-key vault…*, *Add an unlock method…*: unchanged
  (KeysPage.svelte:388-389, 429-434).
- The lock screen's *Finish setting up…* (Lock.svelte:735-737), on a vault that needs setting up
  while its password is on (`st.setupNeeded && st.entangled`, both known while Locked): greyed,
  with `codeText("vault.setup_password_on")` as its title, and the accent passes to *Unlock with
  recovery key*, which also takes the focus (critique 2, UX 3). The core refuses the same before
  any ceremony (2.3), as a backstop. With the password off, the two buttons are as today.
- Inside the dialogs nothing is greyed: the primary is never disabled for a choice, since every
  answer is a valid act (APP.md:2852-2854); the backup tick is absent where it cannot apply, with
  a line that says why, not greyed.
- While an operation runs (a save, verify, compact or archive re-key, `writesRegistry`,
  keys.go:236-242): nothing is greyed, as for every act of this page today; the core refuses at
  begin with `op.in_progress`, "An operation is still writing the vault. Wait for it to finish."
  (strings.ts:335), in the page's error InfoBar.
- While a ceremony runs: its dialog is modal over the page, so no second act can be pressed; the
  core's `ceremony.in_progress` is a backstop, which the fix of section 1 no longer reaches.
- While a retirement is owed and an attempt has failed (the warning `vault.retire_owed`
  standing): nothing is greyed. Each act the barrier holds first tries to finish the retirement
  when pressed (2.4), and the press is how the user asks for that; a refusal comes in the page's
  error InfoBar with `codeText("vault.retire_owed")`, which says what waits and what does not.
  Greying would take away the one press that retries. The content commands of an archive are not
  held and are not touched.

### 3.9 (h) Frontend tests

- frontend/src/lib/rekey.test.ts (new): `backupOwed` (never -> true; 8 days -> true; 6 days ->
  false; 6 days with `vaultRekeyed` -> true; with `keysReplaced` -> true); `backupPossible` (one
  recovery key, removing a YubiKey -> true; one recovery key, removing it -> false; two,
  removing one -> true; none -> false); `backupTicked` (a removal answered "rekey", not owed ->
  true; answered "keep", not owed -> false, owed -> true; a recovery key's removal answered
  "keep", not owed -> true; rotate and replace, not owed -> true); `backupReason` (the re-key's,
  the recovery key's with its label, the replacement's, the owed one's, undefined); `primaryOf`
  over every act, answer and tick, pinned to the 3.1, 3.2 and 3.3 words, the replacement's
  singular for a count of 1; `lastProtector` (the only YubiKey with two recovery keys -> true;
  with another YubiKey -> false; a recovery key -> false); `followUp` (a re-keying removal of a
  YubiKey with the password off -> "replace", on -> "change", leaving recovery keys alone ->
  "add" with `addFirst`, and with the password on `addFirstOn`; a recovery key's re-keying
  removal -> the "other recovery keys" sentences; an enrolment begun from a follow-up after that
  -> "change" or "replace", begun from the page -> null; a re-keying change and a re-keying
  turn-on -> "replace"; in one chain, a replacement then a change -> null (`replaced` holds
  every recovery ID of `after`), a change then a replacement -> null (`changed`); a replacement
  begun from the head with the password on -> "change" with `changeAfterReplace`, with it off ->
  null; the later compromise (finding 6): a chain that replaced the keys, then the chain dropped
  at a lock, then a re-keying change begun from its card with the same recovery IDs in `after`
  -> "replace"; an end whose state has no `after` -> null; a removal answered "kept", a failed
  act, a turn-on without a re-key, a vault left with no recovery key, a recovery key's removal
  from a vault of recovery keys alone -> null); `chainFor` (from the page with a re-key -> a new
  chain with the next id; from the page without one -> null; from a follow-up -> the same
  chain); `chainAfter` (a replacement's `newIds` join `replaced`; a re-keying change sets
  `changed`; a removal changes nothing); `replaceGreyed` (each reason, `replaceNeedsProtector`
  where every method is a recovery key, and undefined); `recoveryNoteOf` (`without` set -> the
  removal's; an enrolment with `withRecoveryKey` -> the switch's; an enrolment on a vault of
  recovery keys alone -> `only` plus `nextMethod`; a removal of one of three recovery keys ->
  `only`; verify, import and records -> their present notes); `replaceResult` (Done ->
  "replaced"; Failed `ceremony.cancelled` after `newIds` -> "notAdded"; Failed
  `vault.indeterminate` -> "unknown"; Failed `vault.conflict` -> "unknown", never "notAdded",
  whether the conflict was the commit's or the retirement's (finding 9); Failed before any
  `newIds` -> null); `replaceNotice` (raised as known by "notAdded" and as unknown by "unknown";
  cleared at Done and at a new replacement's first event; kept across unrelated events; an
  unknown one cleared when `listed` holds every ID and made known when it holds none). And
  (critique 3): `followUp` on a vault left with recovery keys alone — a recovery key's enrolment
  begun from the "add" follow-up -> "add" again, a re-keying change on such a vault -> null,
  never "change" or "replace" (M3); `replaceFacts` from a follow-up after a re-keying turn-on ->
  `changed` true and the count and the switch from `after` though the store's differ, from the
  page -> the store's and `changed` false (M1, N10); `recoveryNoteOf` for a recovery key's
  enrolment begun with the switch -> `enrollRecovery` (M6); `backupReason` for a replacement of
  one key -> `whyReplaceOne` (N1). That *Start again* and the switch keep the act's chain (N9)
  is KeysPage's, which has no component test.
- frontend/src/lib/backup.test.ts: `vaultRekeyed` with no counts -> "behind", `backupBeforeRekey`,
  `suggestExport`; `keysReplaced` -> "behind", `backupKeysReplaced`, winning over
  `backupBeforeRekey`; with counts -> the same `rest`.
- frontend/src/lib/reveal.test.ts: the keep mode's band — before a way, *I have written it down*
  and *Cancel*; after *I have written it down*, a print or a saved file, at once and with no
  second confirmation (R-B), *Next recovery key* for keys before the last and *Continue* for the
  last; after a failed fetch or a stale handle, *Cancel* alone with `keepGone`; *Stay unlocked*
  only in the idle deadline's last minute (`staysUnlocked`); the ordinary showing's band
  unchanged; the keep title for 1 and for 2 keys.
- frontend/src/lib/strings.test.ts:
  - the title and body lists (151, 153) call `removeUnlockMethod.body("Desk key", "hardware")` and
    `body("Paper", "recovery")`; each body at most two sentences, and so `replaceRecoveryKeys.body`
    for 1 and 2; the YubiKey's and the password's body contains "a backup never held it", the
    recovery key's "Copies and backups"; `removeUnlockMethod.title("Password", "2026-09-01")` is
    "Remove Password (added 2026-09-01)?";
  - new `describe("the re-key question and the replacement (DESIGN §5, ruled 2026-10-27)")`: pins
    every `rekeyQuestion`, `rekeyLimit`, `backupAfter`, `primaries`, `replaceRecoveryKeys`,
    `replaceCopy` and keep-mode `revealCopy` value, `ceremonyTitle.password` and `.replace`, the
    `doneRekeyed` values, `doneTitleOf` for remove, password, entangle and replace (1 and 2 keys)
    with and without `rekey` and for rotate, `phaseLeadOf` for each kind and phase (undefined with
    no phase; `replaceRekeyOne` for one key), `followUp`'s sentences, `changeAfterReplace` and
    `retryFirst` among them, `retireHeld` (it names, in this order, the unlock methods, re-keying
    the vault, backups, and creating, importing, re-keying, forgetting, restoring and deleting
    archives, the held acts of 2.4's table, and ends saying the files inside the archives can still
    be changed; critique 3, M2), the `vault.retire_owed` code's copy (it begins with the cause
    "Enfold has not finished clearing" and contains `retireHeld`, and says neither "last change" nor
    "unlock methods is not finished"), `retireNotFinished` with and without `backupToo`,
    `retireLeadOf` for a removal, a re-key, a replacement of 1 and of 2 keys, an enrolment, turning
    the password on and off, and `retireRemnant` with and without a re-key (finding 6),
    `keysNeverAdded` and `keysWorkIfListed` and their `One` forms (with "keep every sheet" and "keep
    both sheets"), `replaceCopy.unknown(1)` and `(2)` (neither says "destroy" nor "could not be
    confirmed"; N2), the `One` forms of `readyLine`, `backedUp`, `noBackup` and `afterFailedBackup`,
    `backupAfter.whyReplaceOne` and `retireWhat.replaceOne` (N1), `revealCopy.keepOpens` (it says
    "by itself" and not "alone"; N6) and `.digitsLineLater(48)` (M6), `vaultCopy.ruleYubiKey` (it
    names a recovery key as a way; M6), `importCopy.replacedOwes` (finding 2), `passwordChange.how`
    (it says there is no field for the old password and that a YubiKey's proof asks the current one,
    and does not say "is not asked for"; finding 11), `replaceRecoveryKeys.when(1)` and `.when(2)`
    (singular and plural), `.ready(10)`, `vaultCopy.replaceNeedsProtector`, `setupLine(1)` and
    `(2)`, and the `vault.retire_owed` and `vault.setup_password_on` codes' copy;
  - no new line ends a sentence on a command's "…" (a search for "…." finds none, critique 2,
    nit 16);
  - every new line that names the act names it as `commandCopy.replaceRecoveryKeys` without its
    "…" or with it, never otherwise (one word per thing);
  - the house-voice list (342-356) and the no-bare-key check (198-212) gain every new line,
    `...Object.values(ceremonyCopy.doneRekeyed)`, `...Object.values(ceremonyCopy.phaseLead)` and
    `...Object.values(ceremonyCopy.followUp)`; the banned-word walk (91-104) covers them by itself,
    `how` included.
- frontend/src/lib/lockscreen.test.ts: `provesFirst`, `choosesVaultPassword` and `nothingChanged`
  over every kind (and `nothingChanged` false for `vault.indeterminate` and `vault.conflict`); the
  dialog views of kind "password" with a YubiKey and with a typed proof, as kind "rotate"'s at
  470-490; `StepConfirm` with a prompt is the view "confirm" and `dialogFoot` gives *Continue*
  and *Cancel* there; `dialogViewOf` answers "done" for a StepDone after a touch when
  `endsWithMore`, and "touch" without it; `dialogFoot` with `recovery` at *No match* gives
  `["recovery", "cancel"]`, and without it `["cancel"]` as today; `offersRecoverySwitch`
  (critique 3, M5): a YubiKey's enrolment on a vault of a YubiKey and a recovery key -> true; a
  recovery key's on the same vault -> true (M6); the second half -> false; begun with the switch
  -> false; a password's enrolment -> false; a removal, a re-key, a replacement, a reveal ->
  false; a vault with no YubiKey, or with no recovery key -> false; no `LastAct` -> false.
- frontend/src/components (no component test exists, so by the lock screen's own lib): the lock
  screen's setup-needed buttons, through a predicate in lib/lockscreen.ts, `finishGreyed(st)`:
  the reason while `setupNeeded && entangled`, undefined otherwise.
- frontend/src/lib/state.test.ts: "reads the backups card again when an act that can re-key ends":
  a `remove` event at Done calls `Vault.LastExportAt` (mocked at 55), and so does a `replace`; an
  `unlock` Done does not. "names the recovery keys a replacement showed": a `replace` event at
  `keep_key` with `newIds`, then Failed with `ceremony.cancelled` -> the store's `replaceNotice`
  holds the ids as known, and still holds them after a status that says Locked; a `replace` Done
  -> null; Failed with `vault.conflict` after `newIds` -> the notice as unknown, never known, and
  after a Reopen and an unlock whose slots list every new ID -> null (finding 9). "decides the
  end from the committed facts, not stale slots" (finding 7): `Keys.Slots` mocked to resolve only
  after the Done, still listing the removed recovery key; a `remove` Done of the last recovery key
  of a vault of two YubiKeys with the password off, whose `after` lists no recovery ID -> no
  follow-up is offered; then two slot reads whose replies arrive out of order -> the later read's
  answer stands (`slotsRead`). "ends the chain at a lock": a chain set, then a status that says
  Locked -> `chain` and `lastAct` null, `replaceNotice` kept.

### 3.10 The mock (tools/uimock/server.py)

- `ceremony_event` (2212-2218): defaults gain `"rekey": False, "phase": "", "without": "",
  "backedUp": False, "backupError": "", "retireError": "", "keepIndex": 0, "keepCount": 0,
  "oldIds": [], "newIds": [], "after": None`; a played Done of the dialog kinds carries `after`
  from the staged vault (`recoveryIds`, `entangled`, `setupNeeded`), so the page's follow-ups are
  decided as the core's are (3.6).
- `begin_act` (2650-2663): refuses with `ceremony.in_progress` while `state["vault"]["ceremony"]`
  is a `DIALOG_KINDS` ceremony not at done or failed, as the core does (keys.go:201-204); it sets
  `rekey` on the parked event from the act's argument (kind "remove" `args[1]`, "password"
  `args[0]`, "entangle" `args[1]`, "replace" always); for kind "enroll" with `args[2]` true it parks
  at the recovery prompt (`("recovery", {"promptId": "drk-1", "method": "recovery"})`) instead of
  the YubiKey's wait.
- `DIALOG_KINDS` (2542) gains "password" and "replace"; the table's 3747209322 becomes
  `begin_act("password")` (2792); the docstrings at 109-129 and 2651-2656 say "password" for
  ChangeEntangledPassword; 3877776464 is `begin_act("replace")` (keys.ReplaceRecoveryKeys).
- **2247223905 (`SetEntangled`, 2756) goes through the ceremony model** (the outside review's
  finding 6): `begin_act("entangle")`, which parks at the proof's wait with `rekey` from `args[1]`
  (on) and the backup path from `args[2]`, so *Turn on…* and *Turn off…* open their panel and can
  be played through by `dialog_scene` (the choose prompt, the phase, the backup's result); the
  switch flips (`state["entangled"]["on"]`) only when the played scene reaches Done, as the core
  commits only then.
- `dialog_scene` (2556-2584) takes `"rekey"`, `"phase"`, `"backupError"`, `"retireError"`,
  `"keepCount"` and the `"without"` knobs, carried on the events it plays, so `{"name": "dialog",
  "scene": "proof_done", "kind": "remove", "rekey": true}` shows the re-keyed end and
  `"backupError": "file.exists"` its bar. `DIALOG_SCENES` (2508-2537) gains `"choose_on"`
  (the turn-on's proof, its choose prompt, Phase "commit", done) and `"replaced"` (the proof's
  touch, Phase "commit", Phase "backup", and `done` with `oldIds`, `newIds` and `backedUp`).
- **The replacement is played, not staged**, as the first way in is (`first_way_in`,
  server.py:2300-2330): `{"name": "dialog", "scene": "replace", "keys": 2}` runs a thread that
  plays the proof's touch, then for each key `ask("replace", "kept", "keep_key", …)` (the helper
  at 2262-2277) with a `/s/` URL the mock serves (2851), an ID, `keepIndex`, `keepCount` and
  `oldIds`, then `ask("replace", "confirm", "confirm", …)`, so the page's own *Next recovery
  key* and *Replace recovery keys* answer it through the shim's /mock/secret (2184-2188,
  2279-2297); then the commit's and the backup's phases and Done. `"cancel_at": 1` (or
  `"confirm"`) ends it there with `ceremony.cancelled`, for the page's bar of 3.3.
- `backup_status` (2100-2111) answers `"vaultRekeyed"` and `"keysReplaced"` from the staged backup
  state (default False), and the inspect answer carries `"rekeyed": False`.
- A staged `"retire_owed": True` (a lock-scene and a dialog-scene knob): the status carries the
  warning `vault.retire_owed`, and the mock's held calls — `Archives.CreateArchive`,
  `RotateKey`, `ForgetArchive`, `RestoreArchive`, `DeleteArchive`, `MergeRecords`,
  `Keys.ExportBackup` and the Keys acts — answer `vault.retire_owed`, while the content calls go on
  as ever (2.4). The `"replace"` scene takes `"fail": "conflict"` or `"indeterminate"`, ending
  Failed with that code after the confirm with `newIds` set, so the page's unknown notice and
  failed view can be seen (3.3). With the knob the status also carries `"retireOwed": True`, so
  that the import's confirmation shows `importCopy.replacedOwes`, and `Vault.InspectRecords`
  answers `vault.retire_owed` at once (2.4); `dialog_scene` takes `"retireRemnant"`, carried on
  the end's event beside `"retireError"`, for the bar's other leads (3.6).
- `begin_act("enroll")` with `args[2]` true parks at the recovery prompt for kind `recovery` as
  for kind `token` (the switch serves both, 2.3), and a played Done of a recovery key's enrolment
  shows the new key in the recovery key's dialog with `digitsLineLater`.
- The lock scenes (server.py:661-690) gain `"setup_needed_on"`: `dict(_AT_REST, setupNeeded=True,
  hasHardwareSlot=False, entangled=True)`, the state a removal of the only YubiKey leaves with the
  password on, for the greyed *Finish setting up…* of 3.8; the mock's `Vault.FinishSetup`
  (2401-2444) answers `vault.setup_password_on` at once while the staged vault is entangled, as
  the core does.
- `dialog_scene` takes a `"follow"` knob only through the page: the end's follow-up is the
  page's (`followUp` over the store's `lastAct` and `chain` and the final state's `after`), so a
  removal played through `begin_act` with the staged vault's `entangled` and recovery slots set
  shows each of 3.6's follow-ups without a knob of its own.

### 3.11 Styles and the files

New CSS goes into the components' own `<style>`, not frontend/src/app.css, which another agent
was editing: RekeyQuestion.svelte carries `.choices`, its legend and `.two` from
ExtractDialog.svelte:131-140; the info line's `.infoline` (143-146) goes into KeysPage.svelte,
EntangledDialog.svelte and ReplaceDialog.svelte; BackupChoice.svelte carries the `.kv` rules the
Re-key dialog uses today (KeysPage's own style); KeysPage's style gains `.ks-head { flex-wrap:
wrap; row-gap: var(--s2) }` scoped to the page (app.css:1475 defines it without wrapping).
Copies of the same rules in two places are a follow-up for app.css once that agent is done.

Files: frontend/src/lib/strings.ts, strings.test.ts, lockscreen.ts, lockscreen.test.ts, rekey.ts
(new), rekey.test.ts (new), reveal.ts, reveal.test.ts, backup.ts, backup.test.ts, api.ts
(`submitSecret`'s kind union gains "kept" and "confirm"), state.svelte.ts, state.test.ts;
frontend/src/components/KeysPage.svelte, EntangledDialog.svelte, CeremonyPanel.svelte,
RecoveryReveal.svelte (keep mode), Lock.svelte (the greyed *Finish setting up…* and the accent's
move, 3.8), Dialog.svelte (the focus effect only), ImportDialog.svelte (two sentences, 3.7),
RekeyQuestion.svelte (new), BackupChoice.svelte (new), ReplaceDialog.svelte (new).

---

## 4. The spec deltas

All text below is verbatim, in the house voice, wrapped at 96 columns. "Replace" quotes the
present text (at b79a10c, line wrapping read as a space); "Insert" names the line it goes after.
The reversal of the backup's order (ruling 1) is recorded in FORMAT §18.3 (4.4), APP.md §13's
rotate dialog and *Start again* (4.1), DESIGN trap 29 (4.3) and DECISIONS (4.5). The rulings on
the second review are recorded where they bind: R-A in APP.md §2.1 and FORMAT R41, R-B in SCOPE,
DESIGN §5 and APP.md §6, R-C's findings in the places 7.6 names, and all four in DECISIONS.

### 4.1 docs/APP.md

**§2.1, Unlocked (lines 87-96).** In the list of mutations (88), after "the entangled password's
switch and change," insert " replacing the recovery keys,". Then replace from "**Rotation
re-derives the Session:**" (line 92) to "forces Locked." (96) — the sentence before it, which
ends "enrolls the first slots.", stays (the outside review's finding 8) — with:

```text
**Rotation re-derives the Session:** `Rotate`, and the calls that rotate in the same commit as a
removal, a password change, turning the password on or replacing the recovery keys
(`RemoveSlotAndRotate`, `ChangeEntangledPasswordAndRotate`, `SetEntangledAndRotate`,
`ReplaceRecoveryAndRotate`, §13), are the only ones that advance the generation, so after any of
them, and after the commit that retires the file's earlier state (FORMAT R41), the core derives
the next Session from the rotating `Unlocked`, installs it, locks the old one, then closes the
`Unlocked`; a lock that lands after the commit and before the install leaves the act done, the
change being on disk, and zeroes the new Session instead of installing it behind a Locked state.
The core's Session accessor returns an error rather than a Session whose `Live()` fails, and an
`ErrStale` reaching a service is an internal invariant violation that forces Locked. **A
retirement left owed is finished at the next unlock** (ruled 2026-10-27): when a change's
retiring commit failed, or the process stopped between the two commits, the vault file still
holds the state before the change (FORMAT R41); the unlock finishes it from the `Unlocked`
before the Session is derived and before any receipt or purge is written. A failure there whose
outcome is unknown is Broken. Any other, there or in the act itself, leaves the vault open and
readable under the warning `vault.retire_owed`, and until the retirement has landed in full —
every commit and every zero-fill of FORMAT R41 it needs — every change that writes a slot, an
archive record or a secret into the vault file, and every backup, waits: a change to the unlock
methods or the password, a re-key, replacing the recovery keys, creating, re-keying, forgetting,
restoring, deleting or importing an archive (the import's inspection included, so that it is
refused before its proof), the purge of forgotten records, an export. Each first tries to finish
the retirement and is refused with `vault.retire_owed` while it cannot; the purge waits for the
next unlock. The files inside an existing archive are not held: adding, replacing, renaming,
moving or deleting them writes only the archive's receipt, no record and no secret, and the
archive's key, which no re-key of the vault changes, already reaches whatever is written there.
A registry write that is not held — a receipt, a verify's hash, an archive's name, description,
hidden flag or path, the vault's lock times — carries a retiring pass in its own flip where it
can, and is made without one where it cannot. Whether a retirement is owed is read from the
vault file's own bytes and kept with the facts the lock screen shows, `RetireOwed` among them,
so it holds across a lock and a Broken state. On a Tampered vault the retirement is finished as
far as it can be without copying the slot region forward (FORMAT R25, R41); only a live copy's
own tail, which a writer before 2026-10-27 could leave, is beyond it, and while that remains
those changes are refused with `vault.tampered` until a good copy is imported (ruled
2026-10-27).
```

**§2.1, a backup adopted (lines 231-241).** After "unknown outcome is Broken, as for any slot
change." insert:

```text
While the vault's password is on, *Finish setting up* is refused before its ceremony
(`vault.setup_password_on`, ruled 2026-10-27): `AddFirstWayIn` writes a first header and cannot
take one whose switch is on, which a vault left with its recovery keys alone by a removal has;
such a vault is unlocked with a recovery key and given a YubiKey with *Add an unlock method…*
on the Vault page, which proves with that recovery key.
```

**§2.1, the import (lines 169 and 189-190).** In the `FileInfo` list, replace
"Generation}` (the last two since §13, so the merge dialog can name which sheet it will want),"
(the end of the code span that opens with `` `FileInfo{Kind `` at line 167) with:

```text
Generation, Rekeyed}` (`RecoverySlots` and `Generation` since §13, so the merge dialog can name
which sheet it will want; `Rekeyed` since 2026-10-27, a file of this vault from a generation
before the vault's own, so the confirmation can say it predates a re-key),
```

and in the confirmation, after "for a backup, that archives registered after the backup's date
are not in it", insert ", and, for a file that predates a re-key (`Rekeyed`), that unlock methods
removed since may still open it and whatever is added to it later". Reflow at 96.

**§2.1, the retired copy (lines 184-185).** Replace "Retired copies are never read by Enfold again
and are the user's to delete; their ways in stay valid for them, which the confirmation says."
with (critique 3, finding 2):

```text
Retired copies are never read by Enfold again and are the user's to delete; their ways in stay
valid for them, which the confirmation says. A vault that still owes a retirement (FORMAT R41;
`RetireOwed` on the status, read from its bytes and kept while Locked) is copied whole, so its
dated copy also opens with any unlock method its last change removed, and with the key a re-key
retired: the confirmation then says that too, and that the copy is best deleted once the new
vault opens (ruled 2026-10-27).
```

**§3, `Status()` (line 806).** Replace "`KeptElsewhere`, `RetiredCopies`, `RetiredPath`)" with
"`KeptElsewhere`, `RetiredCopies`, `RetiredPath`, and since 2026-10-27 `RetireOwed`, the vault
file still owing a retirement, FORMAT R41, §2.1)". Reflow at 96.

**§3, `BeginEnroll` (line 1284).** Replace "- `BeginEnroll(kind, label)` runs the ceremony for the
VMK and releases the key that unlocked —" with:

```text
- `BeginEnroll(kind, label, withRecoveryKey)` runs the ceremony for the VMK — proved with a
  recovery key instead of a YubiKey when `withRecoveryKey` says no YubiKey of the vault is at
  hand, for a YubiKey or a recovery key added to a vault that has a YubiKey and a recovery key
  (§13, *The recovery-key proof*; `params` otherwise) — and releases the key that unlocked —
```

and reflow the bullet at 96.

**§3, `BackupStatus` (lines 837 and 850).** Replace "`BackupStatus() BackupStatus{At, Archives,
Missing, Rekeyed}`" with "`BackupStatus() BackupStatus{At, Archives, Missing, Rekeyed,
VaultRekeyed, KeysReplaced}`", and insert after "so a record older than the last backup and merged
in after it reads as in it." (850):

```text
  `VaultRekeyed` (since 2026-10-27) says the vault was re-keyed after the last backup recorded
  here — the generation `lastExportAt` recorded (§13) is below the vault's own — and
  `KeysReplaced` that none of the recovery keys that backup holds is still one of this vault's.
  Such a backup is at the key the re-key retired, which an unlock method removed since may have
  reached through an earlier copy, or it opens only with recovery keys the user was told to
  destroy; adopting it would continue at that key, so the card asks for a new one (§6). A stamp
  written before the generation and the keys were recorded makes no such claim.
```

**§3, the events (lines 851-855).** Replace the lines from "- Events: `vault.state`" to
"`vault.warning`, and from the shell" with:

```text
- Events: `vault.state` (the whole status), `vault.ceremony {Seq, Step, PromptID, SlotLabel,
  Retries, RetriesKnown, ReaderCount, N, Error, Method, RecoveryID, Rekey, Phase, Without,
  BackedUp, BackupError, RetireError, RetireRemnant, KeepIndex, KeepCount, OldIDs, NewIDs}`
  (`Method` — token · password · recovery — is what the lock screen's wording follows, since an
  adopted pending touch carries no argument on the page; `RecoveryID` rides `StepRecovery` and
  `StepKeepKey` beside the one-time URL in `SlotLabel`, §13; `Rekey` says from the first event
  that the act re-keys the vault in the same commit as its own change, `Phase` what is being
  written once the proof has passed — `commit` or `backup` — `Without` the YubiKey a re-keying
  removal does not ask for, `BackedUp` or `BackupError` how the backup the act was asked to take
  after its change went, `RetireError` why the retirement of the file's earlier state did not
  land and `RetireRemnant` whether only earlier registry ciphertext was left, and the last four
  which new recovery key a replacement shows and the IDs it replaces and adds; `StepKeepKey` and
  `StepConfirm` are a replacement's steps, each with a prompt of kind `kept` or `confirm`,
  answered with no value on the raw channel, §13), `vault.warning`, and from the shell
```

**§3, Keys (lines 1344-1350).** Replace the bullet from "- `RemoveSlot(recipientID)`" to
"`DiscardRecords` (§13, "Merge records")." with:

```text
- `RemoveSlot(recipientID, rekey, backupPath)` (refused before any ceremony, from the cached
  facts that grey *Remove*, with the invariant's reason or `vault.slot_not_found`; `rekey`
  re-keys the vault in the same commit, §13), `RotateNow(backupPath)`,
  `ReplaceRecoveryKeys(backupPath)` (every recovery slot replaced and the vault re-keyed in one
  ceremony and one commit, §13), `ExportBackup(path)` (which records `lastExportAt`, §13; the
  "Export" of earlier drafts) — a `backupPath` that is not empty exports a backup inside the
  act's own ceremony after its change and its retiring commit, skipped while that retirement is
  still owed (§2.1), and the act still ends done when that export fails or is skipped — and
  `VerifyBackup(path)` (§2.1: a staged copy opened with the recovery
  key, nothing kept). What a backup is and whether it is this vault is `Vault.InspectFile`;
  restoring archive records from a backup — the `RestoreArchiveRecord` of earlier drafts,
  re-wrap under the current KWK — is *Import archives from a backup…*: `Vault.InspectRecords`,
  `MergeRecords` and `DiscardRecords` (§13, "Merge records").
```

**§6, the lock screen of a vault that needs setting up (lines 1644-1646).** After "over *Finish
setting up…* (accent) and *Unlock with recovery key* (ruled 2026-10-17)" insert "; while the
vault's password is on, *Finish setting up…* is greyed with "This vault's password is on, so its
setup cannot be finished here. Unlock with a recovery key, then add a YubiKey on the Vault page."
as its title, and the accent and the focus pass to *Unlock with recovery key* (ruled
2026-10-27)". Reflow.

**§6, the lock screen's *No match* (lines 1662-1664).** Replace "(only an unlock can turn to the
recovery key: an import's proof or a create says *Try another YubiKey.*, and a ceremony in a
dialog says the same, its foot band holding *Cancel* alone)." with:

```text
(only an unlock can turn to the recovery key: an import's proof or a create says *Try another
YubiKey.*, and a ceremony in a dialog says the same, its foot band holding *Cancel* alone — save
one, since 2026-10-27: the proof of *Add an unlock method…* adding a YubiKey or a recovery key,
begun without the recovery key on a vault that has a YubiKey and a recovery key, draws the
step's own line and *Use recovery key* (accent) before *Cancel*, as an unlock does, §13 *The
recovery-key proof*).
```

Reflow at 96. (The outside review's second reading, finding 10: without it a builder following
§6 would leave out the button the acceptance walkthrough presses.)

**§6, the PIN's step in a dialog (line 1691).** Replace "— since a dialog offers no recovery
key." with "— since a dialog offers no recovery key there (two cases offer one, neither of them a
PIN's: §13, *The recovery-key proof*, ruled 2026-10-27)." Reflow.

**§6, the Vault page (lines 2194-2202).** Replace "*Add an unlock method…* and *Re-key vault…*
(was *Rotate now…*; its dialog, *Re-key the vault?*, says every unlock method gets the new key
now with no YubiKey needed, and asks for a backup first, §13)." with:

```text
*Add an unlock method…*, *Re-key vault…* (was *Rotate now…*; its dialog, *Re-key the vault?*,
says every unlock method gets the new key now, one of them proving it is you first, and offers
a backup taken afterwards, §13) and *Replace recovery keys…* (§13; greyed with its reason where
the vault has no recovery key, or recovery keys alone, "Add a YubiKey or a password first: a new
recovery key can be shown again only through one."), the three wrapping under the title where
the column is narrow.
```

After "*Remove…* on every card" insert " (its dialog asks why the method is removed and re-keys the
vault on its first, pre-selected answer, §13)", and after "the invariant, "At least 2 unlock methods
are always kept, one of them a recovery key."" insert ", or on a YubiKey's card "To remove this
YubiKey, add a new one or a recovery key first: the vault always keeps at least 2 unlock methods."".
Reflow the bullet at 96.

**§6, the done line (lines 2419-2422).** Replace from "Two endings of the page's own also take
the line:" to "the re-key is the act that ends there)." with:

```text
Endings of the page's own also take the line: *Find missing files* (§13); a backup export,
"Backup saved to E:\backup-2026-10-18.eks" [*Show in Explorer*], said once the export's dialog
is closed on its Done; and, since 2026-10-27, the same line for an act that took a backup after
its change — *Re-key vault…*, *Remove…*, the password's *Change…* and *Turn on…*, *Replace
recovery keys…* — once its dialog is closed on a Done that wrote it (a backup taken before a
re-key, as until then, said none). The line carries no other command: the next step an act's end
offers, such as *Replace recovery keys…*, stands in that end, beside its reason (§13).
```

**§6, the replace confirmation (lines 2490-2491).** After "for a backup, that archives added since
its date are not in it" insert ", and for a file that predates a re-key, "It was made before this
vault was re-keyed: unlock methods removed since may still open it, and whatever is added to it
later.""; and after "says it is kept as a dated copy whose ways in stay valid" insert ", and,
while the vault being replaced still owes a retirement (`RetireOwed`, §2.1), "It also still
holds its state from before its last change, so it opens with any unlock method that change
removed too. Delete it once the new vault opens."" (critique 3, finding 2).

**§6, the backups card (lines 2539-2541 and 2554).** At the end of the *behind* bullet, before its
";", insert: ", or, when the vault was re-keyed after the backup (`VaultRekeyed`, §3), "**Backed
up <since>.** It was made before the vault was re-keyed.", or, when none of its recovery keys is
still the vault's (`KeysReplaced`), "**Backed up <since>.** It opens only with recovery keys this
vault no longer has.", which wins over the re-key's; either stands for the counts". Reflow. In
"The status is read at boot, on unlock, after an export and on `archives.changed`." (2554),
replace "after an export" with "after an export, at the end of an act that re-keys the vault or
replaces its recovery keys,".

**§6, the recovery key's dialog (after line 2601, "lock trigger locks the vault it was shown
on.").** Insert:

```text

**In a replacement** (*Replace recovery keys…*, §13; ruled 2026-10-27) the same dialog shows
each new recovery key before it is the vault's, in keep mode: titled "New recovery key 1 of 2"
("Your new recovery key" for one), the new key's ID beside the title; first the caption
"Replaces <label> · <old ID>.", so the new sheet goes where the old one is; its lead's first
sentence "Once you confirm, this recovery key opens the vault file by itself, and backups made
from then on. Keep it from others."; the digits' caption "48 digits. Nothing changes until every
new recovery key is kept and you confirm."; the three ways, the readback and the bars as above.
Its band holds *I have written it down* and *Cancel* until a way is taken; each way counts at
once, *I have written it down* included, with no second confirmation, as above (ruled
2026-09-09, and held for the replacement on 2026-10-27). Once a way is taken the band holds
*Next recovery key* (accent) — *Continue* for the last — and *Cancel*; the press answers the
ceremony's `kept` prompt and drops the handle. A key that cannot be shown (its one-time link
failed, or its handle is no longer held) says "This recovery key could not be shown here.
Nothing has changed: cancel, and start the replacement again." over *Cancel* alone: before the
commit it cannot be shown again. In the idle deadline's last minute the band carries the subtle
*Stay unlocked*, the status bar's own, which the dialog covers. It is not dismissed by Escape or
the backdrop, as above: *Cancel* is its one way out, so that an Escape pressed in the readback's
fields never costs the sheets already written, and *Cancel* cancels the ceremony: nothing has
been written, and the recovery key on screen never becomes the vault's. After the last one the
act's dialog asks once, its one confirmation, listing one row per key — its label, and "<new ID>
replaces <old ID>" — with *Replace recovery keys* (accent) and *Cancel*: here the vault is not
empty, a new key cannot be shown again before the commit, and the old keys stop opening it at
the commit.
```

**§6, the notice tiers (lines 2668-2674).** In the *attention* bullet, before "a backup missing or
behind", insert "Enfold not having finished clearing the vault file's earlier state
(`vault.retire_owed`, §2.1), ". Reflow.

**§6, Forms (line 3201).** After "(§2.3, ruled 2026-10-17)." insert:

```text
  A form that asks a question and has no field to type in — *Remove <name>?*, the password's
  *Change…* and *Turn on…* — opens with the focus on its question's checked answer (the radio
  checked in its `fieldset`), so that the question and the answer are read before anything else,
  and Enter there presses the submit (ruled 2026-10-27).
```

**§6, Controls (line 3290).** After "where the green would arrive only to leave." insert:

```text
  Once the proof has passed, the lead says what is being written, on whichever ground the
  derivation stood (ruled 2026-10-27): "Removing the unlock method and re-keying the vault…",
  "Changing the password and re-keying the vault…", "Turning the password on and re-keying the
  vault…", "Replacing the recovery keys and re-keying the vault…", "Re-keying the vault…", or
  without the re-key "Removing the unlock method…", "Changing the password…", "Updating every
  YubiKey…"; then, when a backup was asked for, "Saving the backup…". The end keeps its title
  and adds under it, as they apply, read from the left on the neutral panel — an end that adds
  anything is never drawn on the touch's green: a replacement's receipt, one row per key, its
  label and "<new ID> replaces <old ID>", and what to do with the replaced keys' sheets; an
  attention bar when the retirement of the file's earlier state did not land, "The vault file
  still holds its state from before this change: a copy of it taken now still opens with the
  unlock method removed. <why> Until Enfold has cleared it, changes to the unlock methods,
  re-keying the vault, backups, and creating, importing, re-keying, forgetting, restoring or
  deleting archives wait; Enfold tries again at each of them and at the next unlock. You can
  still open your archives and change the files inside them." — its first sentence in the act's
  own words: "with a YubiKey and the old password", "with a YubiKey alone", "with the replaced
  recovery keys", "still holds the vault's old key", none after adding an unlock method or
  turning the password off, and "The vault file still holds earlier versions of its list of
  archives", with ", which the vault's old key reads" after a re-key, where only those are left
  — ending "The backup you asked for waits too." when the act's backup was skipped for it; an
  attention bar when the backup failed, "The backup was not saved. <why> Export one from Backups
  and copies."; and the next step, decided from what the act committed, which its final state
  carries, an info line with a button that closes this dialog and opens the next act's, in one
  chain with the password first (§13), its line ending "Enfold first tries again to finish
  clearing the vault file." while the retirement's bar stands: after a re-keying removal of a
  YubiKey that left only recovery keys, "Add the new YubiKey now; then, if an earlier copy of
  the vault file may be out there, replace the recovery keys." ("…change the password and
  replace the recovery keys." with the password on) with *Add an unlock method…*, which stays
  the next step while the vault has recovery keys alone; after a re-keying removal with the
  password on, "If an earlier copy of the vault file may be out there, change the password, then
  replace the recovery keys." with *Change…*; after a re-keying removal with it off, or a
  re-keying password change or turning on, "If an earlier copy of the vault file may be out
  there, replace the recovery keys too." with *Replace recovery keys…* ("an earlier copy or
  backup … the other recovery keys" after a recovery key's removal); after a replacement with
  the password on, "If an earlier copy of the vault file may be out there, change the password
  too: the vault's own record of it was readable the same way." with *Change…*, unless the
  password was changed earlier in the same chain; and never back to recovery keys or a password
  change the same chain made — a chain being the acts begun from one another's ends, which an
  act begun from the page itself starts anew and a lock ends. A failure of a removal, a password
  change, turning the password on or off, a re-key or a replacement adds "Nothing was changed."
  under its bar, save a save that could not be confirmed or a conflict with another writer,
  which says so in its own words; a replacement that had shown keys adds "The recovery keys
  shown were never added: destroy what you kept of them.", or, when its outcome is unknown — a
  save that could not be confirmed or a conflict, from its commit or the retirement after it —
  "The new recovery keys work only if the Vault page lists their IDs after you reopen it: keep
  every sheet until you have looked.", each in the singular for one key. An act whose commit has
  landed ends done whatever fails after it, save an outcome that is unknown, which is Broken.
```

**§13, the entangled password (lines 3735-3736).** Replace "turning it on asks for the new
password twice, changing it asks for the new one twice," with "turning it on asks whether the
vault file could have been copied before now and then for the new password twice, changing it
asks whether someone else may know the password and then for the new one twice — each re-keying
the vault in the same commit on its first, pre-selected answer (below) —", and reflow at 96.

**§13, the old password (lines 3741-3742).** Replace "**The old password is not asked** (the
ruling of 2026-09-07):" with:

```text
**No field asks for the old password** (the ruling of 2026-09-07; reworded 2026-10-27, since
the proof itself may ask it): the change proves an unlock method first, as every act on this
page does, so proving with a YubiKey asks for the vault's current password with its PIN, as an
unlock does; but the dialog has no field of its own for the old password, and nothing else
asks for it:
```

The rest of the paragraph stands; reflow at 96. The dialog's *How this works* says the same
(strings.ts `passwordChange.how`; the outside review's second reading, finding 11).

**§13, the rotate dialog (lines 3808-3815).** Replace the paragraph with:

```text
**The rotate dialog** — *Re-key the vault?*, opened by *Re-key vault…* (ruled 2026-10-17; it was
*Rotate now…*, which collided with the archive's *Rotate key…*), its body "Every unlock method
gets the new key now; one of them proves it is you first, and the other YubiKeys need not be
present. Backups made before today keep opening with the methods they had." (ruled 2026-10-27;
it said "no YubiKey is needed for this", while the proof is a YubiKey wherever the vault has
one; on a vault with no YubiKey the first sentence ends at "first") and its buttons *Re-key
vault*, or *Re-key, then back up* with the backup ticked, as it is whenever the vault has a
recovery key, and
*Cancel* — shows the last backup's time and offers a backup by the rule every dialog that
re-keys shares (below): it asks, it never refuses to rotate without one, and it never keeps a
copy of the vault (FORMAT §18.3, DESIGN trap 29). **The backup is taken after the re-key**,
inside the rotation's own ceremony once the re-key and its retiring commit have landed
(`RotateNow(backupPath)`: one proof, one dialog), at the new key, and, like every `Keys.Export`,
records `lastExportAt`. Ruled 2026-10-27, reversing 2026-09-07 and 2026-10-17, which took it
before ("no backup means no rotation"): a backup taken before is at the key being retired,
which on the "lost" path must be assumed leaked, and adopting it continues at that key (FORMAT
§15, R28); one taken after is at the new key and holds no removed recovery key.
```

**§13, insert after the rotate dialog's paragraph (after line 3815):**

```text
**Re-keying at a removal, a password change and turning the password on** (ruled 2026-10-27;
DESIGN §5, *Revocation*). *Remove…* opens *Remove <name>?* — "(added <date>)" after the name
where two methods share it — a form. Its body says what stops and what still opens: "Desk
YubiKey stops unlocking this vault now. An earlier copy of the vault file still opens with it; a
backup never held it.", or for a recovery key, the one method a backup holds, "Copies and
backups of the vault made earlier still open with it."; while Enfold's folder keeps replaced
vault files (`RetiredCopies`), a line says that those which had the method still open with it
until they are deleted; where the YubiKey removed is the last of the vault's YubiKeys and no
password remains, a line says "After this the vault has only its recovery keys: add the new
YubiKey here now, or after unlocking with a recovery key." Then it asks DESIGN §5's factual
question, naming the method: "Why are you removing this YubiKey?" (or "this recovery key?") over
*Lost, stolen, or I am not sure*, first and pre-selected, and *I still have it and it was never
out of my control*; a standalone password is asked "Why are you removing this password?" over
*Someone else may know it, or I am not sure* and *Only I have ever known it*, since a password
is not lost but known. Under each answer a second line says what it does, "The vault is
re-keyed, as recommended." or "The vault keeps its key."; the dialog opens on the checked answer
(§6, Forms). Under the question an info line says the limit plainly, whichever the answer:
"Re-keying protects the future, not the past: whatever someone could open until now, they may
already have copied.", and, while the vault has a recovery key besides any removed, what is left
to do about it: whoever opened an earlier copy with the method could also read the recovery keys
— and, with the password on, the vault's own record of the password — so the user changes the
password afterwards and then replaces the recovery keys with *Replace recovery keys…* (below);
for a recovery key, whoever has it can open an earlier copy or backup and read the other
recovery keys there, so *Replace recovery keys…* is the act to take instead, which removes this
one too, and the user checks the unlock methods and removes any they did not add (FORMAT R38).
*Change…* opens *Change the password for every YubiKey?*, whose body says one unlock method
proves it is you first and the other YubiKeys need not be present, and which asks "Why are you
changing the password?" with the standalone password's two answers; *Turn on…* asks "Could the
vault file have been copied before now?" over *Yes, or I am not sure* and *No, it was never
copied*, since a copy made before the switch keeps opening with a YubiKey alone; each has the
info line in its own words, pointing to *Replace recovery keys…*. *Turn off…* asks nothing and
offers no re-key: the file then opens without the password anyway, so a re-key would revoke
nothing. The first answer makes the act and the re-key one commit (FORMAT §8), so a crash, a
failure or a lock leaves the old state whole or the new one; every act that writes the slot
region, the re-key and the replacement among them, is followed in its ceremony by the commit
that retires the file's earlier state (FORMAT R41), and when that does not land the act still
ends done, saying so, takes no backup, and the changes §2.1 names wait until it lands, each
trying to finish it first, as the next unlock does; the files inside the archives do not wait. A
removal the invariant would refuse is refused before any ceremony. A re-keying removal of a
YubiKey does not ask for the YubiKey it takes away: the wait names the other YubiKeys, and where
none remains and no standalone password does, the proof is one of the vault's recovery keys; a
password's removal is proved by typing a password, the one removed included. **The backup** is
one rule in the five dialogs, the rotate dialog's and the replacement's included: the *Last
backup* fact, a reason, then *Export a backup afterwards*, drawn whenever a recovery key remains
after the act and ticked on every answer that re-keys — the act's own re-key leaves every
earlier backup at the old key, which the backups card then calls behind — at a recovery key's
removal on either answer, since the backups made while it was enrolled hold it, and otherwise
while no backup is recorded here, the last is over a week old, was made before the vault was
last re-keyed or holds only replaced recovery keys; where no recovery key would remain, "No
backup of this vault can be made without a recovery key." stands in its place. The tick follows
the answer as it changes. Each primary names what its press does, the backup included: *Remove
and re-key*, *Remove unlock method*, *Remove, re-key and back up*, *Remove, then back up*, and
likewise *Change…*, *Turn on…*, *Re-key, then back up* and *Replace, then back up*. The backup
is written inside the act's ceremony after its change has committed and been retired, from the
re-keyed vault; a Save dialog cancelled at the press changes nothing, and an export that fails
leaves the act done, its end saying the backup was not saved and why. Each act proves an unlock
method once and ends as what is now true: "Unlock method removed and vault re-keyed", "Unlock
method removed", "Password changed and vault re-keyed", "Password changed", "Password turned on
and vault re-keyed", "Vault re-keyed", "Recovery keys replaced and vault re-keyed". **The ends
point onward in one chain, the password first**: a re-keying removal's end offers *Add an unlock
method…* where a YubiKey's removal left the vault with recovery keys alone, else *Change…* where
the password is on, else *Replace recovery keys…*; a re-keying password change's or turning on's
end offers *Replace recovery keys…*; the replacement's end offers *Change…* where the password
is on and was not changed earlier in the chain, and otherwise nothing; and an end never points
back to recovery keys or a password change its own chain made, a chain being the acts begun from
one another's ends: an act begun from the page itself starts a new one, so that a later exposure
gets the whole advice again (§6, Controls). Each end decides from what its act committed, and
each says when the step is needed: "If an earlier copy of the vault file may be out there". Each
act is its own commit, not one repair: until the chain's last act lands, the recovery keys an
earlier copy held still open the vault, and whoever holds one reads what the vault file holds
meanwhile.

**Replace recovery keys…** (ruled 2026-10-27; DESIGN §5, the table's fourth row; FORMAT R38). In
the head of *Unlock methods*, beside *Add an unlock method…* and *Re-key vault…*; greyed while
the vault has no recovery key ("This vault has no recovery key to replace.") or only recovery
keys ("Add a YubiKey or a password first: a new recovery key can be shown again only through
one."; the core refuses with `vault.setup_needed`: no protector would remain to show a new key
again, and adding one is the next step anyway). Its dialog, *Replace the recovery key?* or
*Replace the 2 recovery keys?*, says that Enfold makes as many new keys as the vault has, one
for each, that the old ones stop unlocking this vault and that copies and backups made before
still open with them; when it is needed — an earlier copy of the vault file, or a backup, may
have been opened by someone else, who could then read every recovery key in it — and, since a
replacement replaces recovery keys only, to remove first the unlock method that copy was opened
with, if it was a YubiKey or a password, and any unlock method the user did not add; with the
password on, to change the password too, unless a re-keying password change or turning the
password on came earlier in the same chain; to have somewhere to keep each new recovery key
before starting, and that Enfold's idle lock, after the vault's idle time without input, ends
the replacement with nothing changed; the backup block, always ticked, since every earlier
backup then opens only with replaced keys; and *Replace recovery keys* (accent) or *Replace,
then back up*, and *Cancel*. `Keys.ReplaceRecoveryKeys(backupPath)` is one ceremony of kind
`replace`: the proof (a YubiKey, else the password; never a recovery key); then, for each old
key in the order the page lists them, a new key made with the old one's label and shown in the
recovery key's dialog in keep mode (§6) until the user has kept it (`StepKeepKey`, the prompt
`kept`); then the confirmation (`StepConfirm`, the prompt `confirm`), one row per key, its label
and which new ID replaces which old one; then one commit that adds the new recovery slots,
removes every old one and re-keys the vault, the new keys' copies kept under the new key only
(FORMAT §8), so none is ever readable with an old one; then the commit that retires the earlier
state; then the backup, after. A cancel, a lock or a failure before the commit changes nothing —
the act at once retiring whatever a failed commit wrote of the new keys, so that no sheet the
page says to destroy still opens anything — and the page names the keys shown and never added —
in the failed dialog, and in a bar on the Vault page that stays until closed, across a lock — so
their sheets can be destroyed; every one-time link the act made is dropped at its end, whatever
the end; an outcome that is not known — a save that could not be confirmed, or a conflict with
another writer, which may come after the replacement itself committed — is Broken, and the
dialog and the Vault page's bar say to keep every sheet until, after *Reopen*, the Vault page's
IDs say which keys are the vault's, never to destroy them; the bar then settles itself by those
IDs. The end lists the same rows and, by what the backup did, says to destroy the replaced keys'
sheets and files once the new backup opens, or to export one now; of the backups made before, it
says that they still open with the replaced keys and that this vault can still import archives
from them (FORMAT §18.2), and leaves them to the user. Its next step is *Change…* where the
password is on and was not changed earlier in the chain; otherwise it offers none. The backups
card then says, until a new backup is recorded, that the last one "opens only with recovery keys
this vault no longer has".

**The recovery-key proof** (ruled 2026-10-27, narrowing 2026-10-17's "the dialogs offer no
recovery key"). An act on this page proves with a recovery key in two cases only, besides a
vault whose methods are all recovery keys, which proves every act with one as before. A
re-keying removal of the vault's only YubiKey, with no standalone password: the YubiKey removed
is not asked for and nothing else proves, so the proof is one of the vault's recovery keys. And
*Add an unlock method…* adding a YubiKey or a recovery key on a vault that has a YubiKey and a
recovery key: under the opening wait for a YubiKey of the vault stands *Use recovery key
instead*, and at *No match* *Use recovery key* is the accent with the step's own line, as on the
lock screen (§6 names this exception); pressed, the act is cancelled and begun again with
`BeginEnroll(kind, label, true)`, the dialog staying up, and the proof is the recovery key. A
new YubiKey then comes at once, with no YubiKey to take out first, and the end says the next
step: remove the lost YubiKey with *Remove…*. A new recovery key is shown as any added one is,
its caption saying that it can be shown again only after a YubiKey or a password is added;
*Remove…* on the lost YubiKey is then enabled, which is how a user with no spare YubiKey yet
withdraws the lost one. No other act, and no other moment of these two, offers a recovery key;
the showing of a recovery key is still authorised by a YubiKey or a password and never by a
recovery key (DESIGN §5), though that YubiKey may itself have been added with a recovery key,
which is why the replacement asks for unknown unlock methods to be removed. So the case the
feature exists for — one YubiKey, lost, and one recovery key — goes: unlock with the recovery
key; add the new YubiKey, proving with the recovery key; remove the lost one, answered *Lost,
stolen, or I am not sure*, proving with the new YubiKey and re-keying; with the password on,
change it; then replace the recovery key. With no new YubiKey yet, a second recovery key added
the same way lets the lost one be removed now.
```

**§13, `lastExportAt` (lines 3821-3827).** Replace the paragraph with:

```text
**`lastExportAt`** is the settings file's (§3): an object `{vaultId, at, gen, recovery}` — the
hex `vault_id` of the vault exported, Unix seconds, and since 2026-10-27 the VMK generation
exported and the hex IDs of the recovery slots the backup holds (a stamp without them makes no
claim about either) — written only by `Keys.Export` and by the backup an act takes after its
change. It reads as *never* when absent, zero, ahead of the clock, or carrying another vault's
id, and *never* makes the confirmations above say that no backup is recorded here rather than
name a date. It is a local, unauthenticated convenience: it chooses wording, ticks the backup of
the dialogs that re-key and dates the backups card's status, which also says when the vault was
re-keyed after it or its recovery keys were replaced (§6, through `Vault.BackupStatus`, §3), and
it never gates a cryptographic operation.
```

**§13, the acts' ceremonies (lines 3829-3846).** Replace "*Remove <name>?*, the rotate dialog or
the password's own dialog is answered" with "*Remove <name>?*, the rotate dialog, the password's
own dialogs or *Replace the recovery keys?* is answered". In the title list, replace "*Password
with every YubiKey*" with "*Password with every YubiKey* (turning it on or off), *Change the
password for every YubiKey*, *Replace the recovery keys*"; after "the YubiKey where the vault has
one, else its password (`acquireUnlocked`)" insert ", not asking for the YubiKey a re-keying
removal takes away, and a recovery key in the two cases of *The recovery-key proof* (above)";
after "end in the recovery key's dialog (§6), which takes over from the act's" insert "; a
replacement's new keys are shown in it before its commit, and the act's dialog returns for the
confirmation and the end"; and replace lines 3845-3846, from "with what it was begun with — the
rotation after a failed backup" to the end of the paragraph, with:

```text
with what it was begun with — an act that failed changed nothing and wrote no backup, so it
begins again whole, its backup to the same file, and a replacement with new keys (ruled
2026-10-27, reversing 2026-10-17's "no backup means no rotation": the rotation's backup came
first, and *Start again* after a failed backup began with the backup again).
```

**§13, bound methods (lines 3880-3881).** Replace from "`Keys.SetEntangled(on)` and" to "the
standalone password and the" with:

```text
`Keys.SetEntangled(on, rekey, backupPath)` and `Keys.ChangeEntangledPassword(rekey, backupPath)`
(ceremonies of kind `entangle` and, since 2026-10-27, `password`), since 2026-10-27
`Keys.ReplaceRecoveryKeys(backupPath)` (a ceremony of kind `replace`) and the third argument of
`Keys.BeginEnroll(kind, label, withRecoveryKey)` (the standalone password and the
```

and reflow the rest of the paragraph at 96.

**§13, the one press (lines 3900-3901).** Replace from "The rotate dialog's pre-selected backup
performs the export and then the rotation in one press;" to "rotates nothing." with:

```text
The rotate dialog's backup is taken in the same press and the same ceremony as the rotation,
after it (until 2026-10-27 the page began the rotation while the export's ceremony still ran,
which the core refused, so the vault was never re-keyed); a Save dialog cancelled at the press
rotates nothing.
```

Left as they are, and still true: §3 `Slots()` and `Removable` (1279-1283), the slot-change guard
(1351-1357), §6 *Controls*' ceremony in a dialog and its *Start again* (3252-3303, save the
insert above), §6's list of dialogs (2265-2267), §13's `Removable` and `CanEnable` (3750-3760).

### 4.2 docs/SCOPE.md

**Line 45.** Replace "- VMK rotation, pre-selected on removals and password changes" with "- VMK
rotation, pre-selected on removals, password changes and turning the password on", as DESIGN's
trap 11 now says (4.3; critique 3, N11). DECISIONS.md:5245 and the commit message of 5814d9a say
"SCOPE:44"; the line is 45, the slot invariant's bullet (44) standing before it.

**Lines 41-43.** Replace "every showing offers save as a text file, print, or written down with a
second confirmation (ruling 2026-09-07)" with (R-B):

```text
every showing offers save as a text file, print, or written down, each counting at once with
no second confirmation (ruled 2026-09-09, superseding 2026-09-07's; held for every showing on
2026-10-27, a replacement of the recovery keys included, which asks once, before its commit,
with each new key beside the one it replaces)
```

and reflow the bullet at 96.

**Insert after line 45:**

```text
- Replacing every recovery key in one act, which re-keys the vault in the same commit, each new
  key shown and kept before it (ruled 2026-10-27; `FORMAT.md` R38)
```

### 4.3 docs/DESIGN.md

Section 5 keeps its argument; two table cells are corrected, one paragraph is added, two are
reworded (the one on deferred rotation and the TRIM paragraph), traps 11 and 29 are reworded, and
its paragraph *Kept once more, under the VMK* (lines 335-351) is brought to 2026-09-09's words
(R-B) and to the page's name.

**The second showing (line 344).** Replace "from the Keys page" with "from the Vault page": the
page's name (strings.ts:10-11), which the paragraph's own new sentence below already uses
(critique 3, N4).

**The reveal (lines 350-351).** Replace "the last two are confirmed a second time, since the page
cannot tell a print from a cancelled one and the first click is a reflex." with:

```text
each counts at once, with no second confirmation (ruled 2026-09-09, superseding 2026-09-07's:
a user set on leaving is not stopped by one, the key can be shown again from the Vault page,
and at the moment it is first shown the vault holds nothing yet). A replacement of the recovery
keys, whose new keys cannot be shown again before its commit, asks once before that commit,
listing each new key beside the one it replaces (ruled 2026-10-27, `APP.md` §13).
```

Reflow at 96.

**The table's second row (line 379).** Replace "**All future access.** The removed credential now
yields only the old VMK, which opens nothing in the current keystore" with:

```text
**What the removed credential reaches from now on.** It yields only the old VMK, which opens
nothing in the current keystore — but an earlier copy it opened also gave up that day's
recovery keys and `K_P`, which the rows below take back, and the keys of archives that existed
then, which a VMK rotation never changes (`FORMAT.md` §8)
```

(the outside review's finding 1: an earlier copy's escrowed recovery keys open the re-keyed vault,
FORMAT.md:733-736, so "all future access" was true only with the fourth row done too).

**The table's fourth row (line 381).** Replace "a new key to write down, per slot" with "a new key
to keep, per slot; one act, *Replace recovery keys…*, which rotates in the same commit (`APP.md`
§13)".

**Insert after line 409 ("to rotating on any password change."), before line 411:**

```text
**The change and its rotation are one commit, and the file keeps no state before it** (ruled
2026-10-27). The slot record goes, or the new password's `K_P` arrives, in the same superblock
flip that installs the new VMK (`FORMAT.md` §8), so a crash, a failure or a lock leaves the old
state whole or the new one, never a removal or a password change without the rotation it was
asked for; and a second commit at once retires the slot region and the superblock copy that
still name the state before (`FORMAT.md` R41), which the withdrawn credential would otherwise
open inside the vault file itself. Should that commit fail, the bytes themselves record that it
is owed: no change that writes a slot, an archive record or a secret, and no backup, lands until
it does, each trying to finish it first, while the vault stays readable and the files inside its
archives can still change; the next unlock finishes it before the vault is used. That commit,
and every commit after it, also zero-fills the registry ciphertext no superblock copy names any
more, which the retiring VMK would otherwise still read (`FORMAT.md` R41). On screen the
question names the method, "this YubiKey" or "this recovery key", since a YubiKey is never a
bare "key" there, and the re-keying answer stands first. A password, standalone or entangled, is
asked whether someone else may know it (`APP.md` §13). Turning the entangled password on is a
credential change too: a copy of the vault file made before the switch keeps opening with a
YubiKey alone, so the rotation is offered, pre-selected, asked as whether the file could have
been copied. Turning it off offers none: the file then opens without the password anyway, so a
rotation would revoke nothing. Replacing the recovery keys is an act of its own, never folded
into a removal: it makes and shows the new keys first, each kept as any recovery key is, asks
once with each new key beside the one it replaces, and then, in one commit, adds them, removes
every old one and rotates, so no new key is ever kept under a VMK an exposed one still opens;
the removal and the password change point to it in one chain, the password first, saying when it
is needed. Where no YubiKey can prove a slot change — the vault's only YubiKey is the one
removed, or the user says at the enrolment that it is lost — a recovery key may (ruled
2026-10-27). It still never authorises a showing of an escrowed key, though a YubiKey enrolled
with it can: so the replacement also asks the user to remove any unlock method they did not add.
```

**Lines 411-412.** Replace "so a rotation that commits is complete (`FORMAT.md` §8)." with "so a
rotation that commits re-wraps every way in at once (`FORMAT.md` §8); what the file still holds
of the state before is retired next (`FORMAT.md` R41)." (critique 3, N4: the paragraph inserted
just before it says a second commit retires that state, and the outside review's Q4 asked that
revocation not be called complete at the first flip.)

**The TRIM paragraph (lines 414-416).** Replace the paragraph with (critique 3, M9: the table's
second row now says that the old VMK's earlier copy gave up that day's recovery keys and `K_P`,
which an old registry recovered from unallocated blocks also holds, so "useless against the
current keystore" was true only with the table's last rows done, and R41's zero-fill is not
physical erasure):

```text
**This does not depend on TRIM or secure erasure, once the table's last rows are done.** After
rotation, a slot record recovered from unallocated SSD blocks decrypts to the *old* VMK, which
opens nothing in the current keystore by itself; an old registry recovered beside it is an
earlier copy like any other, whose recovery keys and `K_P` the table's last two rows take back.
Within the vault file the writer leaves neither (`FORMAT.md` R41); on the medium beneath it the
old bytes may stay, which is why those rows matter. Wear levelling and TRIM only become relevant
at the third tier.
```

**Trap 11 (lines 759-762).** Replace "Rotation is pre-selected on every removal and every password
change, and since Revision 2 none is deferred (§5, Revocation)." with "Rotation is pre-selected on
every removal, every password change and turning the password on, and since Revision 2 none is
deferred (§5, Revocation)."; and replace "replacing every recovery slot does." with "replacing
every recovery slot does — one act that rotates in the same commit (`APP.md` §13)."

**Trap 29 (lines 1001-1003).** Replace with:

```text
29. **A pre-rotation copy of the vault re-opens the slot you just removed.** Rotation exists to
    revoke; a "keep the old file" safety net keeps the revoked way in alive, and so does the
    file's own losing superblock copy and slot region copy until they are retired (`FORMAT.md`
    R41). The safety net is a backup — recovery slots only — which every dialog that rotates
    offers to take right after the rotation, at the new key (APP.md §13): one taken before is at
    the key being retired, and adopting it would bring that key back (ruled 2026-10-27; until
    then the backup was taken before).
```

### 4.4 docs/FORMAT.md

No layout change. One writer rule is added (R41); §4 gains the registry's zero padding and
zero-fill; §5's `slot_region_len` cell, R25, R28, R38, §8 and §15 gain a sentence each or have one
reworded; §18.3 is reworded. R41 names no reader rule: a reader of the keystore is unchanged.
With these deltas FORMAT nowhere calls registry ciphertext that no superblock names inert
(revision 3's R41 did; the outside review's second reading, finding 1).

**§4, after "A new registry never lands on the committed one." (after line 882, "which is R31's
guarantee one level up."), insert:**

```text
**Nor does a registry the two superblock copies no longer name stay in the file** (R41). Every
registry write fills the rest of its last 4 KiB unit with zeros, never past the file's physical
end before the commit nor into the live registry's extent; and once the flip is durable and the
file trimmed to the later named registry's end, the writer reads both superblock copies again
and, if no other writer has committed since, writes zeros over every 4 KiB unit from `0x42000`
to the file's physical end that holds no byte of either named registry and holds a nonzero byte.
A unit that holds any byte of a named registry is never written, so the zero-fill never rewrites
a sector a reader of either state reads (the 4 KiB unit is the atomic-write unit, §17), and it
writes no superblock, so it has no commit point: cut short, it leaves only bytes no superblock
names, which the next commit's zero-fill or the next unlock's retirement clears. Such bytes are
not harmless: the registry is AES-256-GCM, a counter mode, so whoever holds the Metadata key a
registry was sealed under and one block of its plaintext — every registry of a vault begins with
the same sixteen bytes — reads the rest.
```

**§4, "Why the slot region is doubled too" (line 895).** Replace "1. write the new slot region into
the inactive copy, fsync" with "1. write the new slot region into the inactive copy, the rest of
the copy zeroed (R41), fsync".

**§5, the `slot_region_len` cell (line 927).** After "and is not covered by `slot_region_hash`
(R25)" insert "; a writer reads it only to decide whether a retirement is owed (R41)".

**R25 (lines 471-476).** After "The two are never out of step, because they land in one flip."
insert " R41's retiring commit is the one write of the slot region that carries the hash forward
rather than recomputing it: it writes the live region byte for byte into the other copy, so the
hash already vouches for those bytes, and it is made only while the live region verifies against
it." (critique 2, F9.)

**R28 (lines 543-545).** Replace "an export or a copy that may have leaked is answered by replacing
every recovery slot of its date, then rotating (§15, `DESIGN.md` §5)." with "an export or a copy
that may have leaked is answered by replacing every recovery slot of its date and rotating, in
one commit, and by changing the entangled password, with a rotation of its own, if the vault has
one (§8, §15, `DESIGN.md` §5)." The sequence it named, replace then rotate, is the one §8 now
refuses to split, and it left the password out (critique 2, F9).

**R38 (lines 733-736 and 742-743).** Replace "and the answer to a suspected VMK exposure is
replacing every recovery slot, changing the entangled password if the vault has one — `K_P` sits
under the same `KWK_secrets` (§7.6) and, like the digits, survives every rotation — and then
rotating (`DESIGN.md` §5)." with:

```text
and the answer to a suspected VMK exposure is replacing every recovery slot and rotating in one
commit — the new slots' `recovery_escrow` records sealed under the new `KWK_secrets` only, so no
new key is ever readable with an exposed one (§8) — and changing the entangled password if the
vault has one, with a rotation in the same commit too: `K_P` sits under the same `KWK_secrets`
(§7.6) and, like the digits, survives every rotation (`DESIGN.md` §5).
```

Then replace "written by the commit that creates the slot (creation, `AddSlot`)," (line 727) with
"written by the commit that creates the slot (creation, `AddSlot`, or a replacement of the
recovery slots, which seals it under `KWK_secrets'`, §8),"; and replace "creation, `AddSlot`,
`RemoveSlot`, `Rotate`, an export" with "creation, `AddSlot`, `RemoveSlot`, `Rotate` and a
rotation that carries a change (§8), an export".

**R41, insert after R40 (after line 829, "than every hole before it."), before the "---" of line
832:**

```text
**R41 — A writer retires the state before a slot region write, and leaves no registry no
superblock names.** The keystore's two superblock copies alternate (§4), so after any commit the
copy that lost still names the previous state: its slot region copy and its registry, intact and
kept addressable on purpose. After a commit that wrote the slot region — a slot added or
removed, the header changed, a rotation — that state can hold a way in the commit withdrew: a
removed slot's record, the hardware slots under the old `K_P` or under none, a replaced recovery
slot, and through that state's registry every archive key and every escrowed recovery key of its
moment (R38); after a rotation it is also the one state the retiring VMK still opens. A
registry-only commit does not end it: it names the live region and leaves the other copy's bytes
as they were. Nor is a registry gone when no superblock copy names it: its ciphertext stays
where it was written until a later write lands on it, and the registry is AES-256-GCM, a counter
mode, so whoever holds the Metadata key it was sealed under and knows one block of its plaintext
— every registry of a vault begins with the same 16 bytes, its format version and the start of
`device_id` — recovers the counter, and with it the nonce, and reads the rest. Nothing need be
damaged to use any of this: whoever holds the file can read it. So **every slot region write
fills the rest of its 128 KiB copy with zeros; every registry write fills the rest of its last 4
KiB unit with zeros**, never past the file's physical end before the commit nor into the live
registry's extent; **after every commit's flip and trim, the writer writes zeros over every 4
KiB unit of `[0x42000, the file's physical end)` that holds no byte of the registry either
superblock copy names** and holds a nonzero byte. The physical end is the end as far as the
writer has written, which a commit that failed before its flip after writing its registry past
the last flip's end has moved, so the writer keeps it rather than that last end: the trim cuts
to the later named registry's end, and the zero-fill covers what a failed trim leaves. The
zero-fill never writes a unit that holds such a byte, so never a sector a reader of either state
reads (the 4 KiB unit is the atomic-write unit, §17), and never a superblock, so that it has no
commit point; it is not made when the superblock copies, read again first, show that another
writer has committed since the flip. And the writer follows a commit that wrote the slot region
at once with a **retiring commit**: the live slot region, byte for byte, written into the copy
the live superblock does not name, the rest of that copy zeroed, and the registry re-sealed at
the same generation with only its `modified_at` advanced (R35) and its `slot_region_hash`
carried, which the writer does only while the live region verifies against that hash (R25); its
superblock goes to the losing copy, as every commit's does, and its zero-fill follows. The live
copy is never written in place (§4): a live copy that holds bytes past `slot_region_len`, or a
live registry whose last 4 KiB unit holds bytes past its tag, as a writer before this rule could
leave, is settled by a second retiring commit once the first has left it no longer live. **A
retirement is owed** exactly while the live slot region copy holds anything past
`slot_region_len` but zeros; or the other copy holds anything but the live region followed by
zeros, or zeros alone; or the losing superblock copy fails its checksum, carries a
`vmk_generation` other than the live one's, or names a slot region copy whose bytes `[0, its
slot_region_len)` are not exactly the live region; or a 4 KiB unit of `[0x42000, the file's
physical end)` holds a nonzero byte outside both named registries. The predicate is over the
file's own bytes, so it survives a failed retiring commit — one that wrote its bodies and lost
its superblock write included — a crash between the two commits, a zero-fill cut short, a reopen
and a copy of the file, and it trusts no field anyone could clear: `rotation_pending` stays
reserved (§5). While one is owed, every registry-only commit the writer makes carries one
retiring pass in its own flip, or, when that pass cannot be written, is made as the plain commit
it is; and the application writes no slot, no new, removed or forgotten archive record and no
secret, and exports no backup, before the retirement has landed in full (`APP.md` §2.1); the
writer finishes it after unlocking, before the vault is used. A slot region that fails R25 is
never written forward: in place of each retiring commit the writer then makes a registry-only
commit, which carries the hash it was given as every registry-only commit does, and, once both
superblock copies name the live slot region copy, writes zeros over the other copy, which
neither names, and makes the zero-fill; what can then remain is a live copy's own tail, and
while it does the application refuses what it holds until a copy is imported (`APP.md` §2.1).
The retiring commit decides nothing the first did not; its failure does not undo the first. What
it does not change: a reader still chooses a superblock copy by its checksum and `seq` alone
(§4) and reads the region and the registry that copy names, with no fallback on a damaged body;
with the live superblock copy damaged, the other opens the same state after a retirement, and
the state one commit behind otherwise. What it does not establish: physical erasure of the old
bytes, which an SSD may keep, as it may keep the space a trim gave back (`DESIGN.md` §5), or
anything about copies of the file taken before it landed, the dated copy included that an import
keeps of a vault that still owes one, which the import's confirmation names (`APP.md` §2.1). A
rotation's own atomicity is §8's and is not changed by it. The word is this rule's own: a
retiring commit is a full commit, neither R31's retired copy, a superblock copy carried at `seq`
− 1 onto the live state, nor a slot whose `slot_state` is 2.
```

**§8, the opening (lines 1275-1276).** After "`DESIGN.md` §5 covers why the default sits there
rather than on "skip"." insert " Turning the vault's password on is a credential change in this
sense; turning it off is not: the file then opens without the password, so a rotation would
revoke nothing (`DESIGN.md` §5)." Reflow.

**§8, after the paragraph "The whole rotation lands in one superblock flip, so it is atomic…"
(lines 1301-1304), insert** (unnumbered, so that the rotation's own steps stay one flip;
critique 2, F12):

```text
After the flip the writer retires the state before (R41) with a second commit at the new
generation: until it lands, the losing superblock copy and the other slot region copy still hold
the state step 5 replaced, which the retiring VMK opens and whose slot region still holds every
slot of before, a removed one included, and its zero-fill clears every earlier registry no
superblock names. It is a commit of its own and changes nothing above: a crash between the two
leaves the new state live and the retirement owed.
```

**§8, insert after line 1321 ("reach."), before the "---" of line 1323:**

```text
**A rotation may carry a removal, a new password or new recovery slots** (ruled 2026-10-27). The
slots of step 4 are the records this commit writes: a removal that rotates deletes its record
from that set before step 3 prunes against it, so the removed slot's `recovery_escrow` record is
dropped rather than re-encrypted, and the slot is never wrapped the new VMK. A replacement of
the recovery slots deletes every recovery record and adds the new ones, whose public keys were
derived from their keys before the commit; step 3 drops the old records' escrow and seals the
new ones' under `KWK_secrets'` alone, and step 4 wraps the new VMK into them as into every
surviving slot, so no new recovery key is ever escrowed under a VMK an old one opens. A password
change that rotates, and turning the password on with a rotation, write their new slot region
header, `entangle_salt` included, in step 5. In step 3 the `entangled_key` record, where there
is one, still opens under the retiring `KWK_secrets` — a record that does not open fails the
rotation — and its plaintext is discarded; the record written holds the new password's `K_P`
sealed under `KWK_secrets'`, created when the switch was off, and step 4 wraps every hardware
slot from it. The old `K_P` is in no record of the new state. Each is still one superblock flip:
a crash leaves the old state whole or the new one, never a removal, a password change or a
replacement without its rotation; R41's retiring commit then retires the state before, after
which no copy in the file still holds a state the removed credential, the old password or an old
recovery key opens.
```

**§15 (lines 1628-1631).** Replace "A copy that may have leaked is therefore answered by replacing
every recovery slot that was active when it was made — not only the one it was opened with —
changing the entangled password, which is the only one of the three steps that replaces `K_P`,
and then rotating." with:

```text
A copy that may have leaked is therefore answered by replacing every recovery slot that was
active when it was made — not only the one it was opened with — in one commit with a rotation,
and by changing the entangled password, which is the only step that replaces `K_P`, with a
rotation of its own (§8).
```

**§18.3 (lines 1846-1850).** Replace the heading's paragraph with:

```text
§8 stands: slot region, registry, then the superblock flip, atomic. It is not a new file renamed
over the old, and no pre-rotation copy is kept, in the file or beside it: a rotation usually
follows a removal, and the copy would still open with the slot just removed, so the state the
losing superblock copy and the other slot region copy hold is retired at once (§8, R41).
The safety net is the backup the app offers to take right after the rotation (`APP.md` §13): at
the new generation, holding the recovery slots only. Until 2026-10-27 the app asked for it
before rotating; it was moved after, since a backup made before a rotation is at the retiring
VMK — it still opens with the recovery slots it holds and is read through `vmk_history` (§18.2),
but adopting it continues at that VMK, which a removed credential may have reached through an
earlier copy — so the app says when the last backup predates a re-key and asks for a new one.
```

### 4.5 docs/DECISIONS.md

**Append at the end of the file:**

```text
**VMK rotation at a removal and a password change, and replacing the recovery keys: the design**
(2026-10-27). The entry above ruled the rotation built; this is how, after the designer's six
critiques, two outside reviews and the owner's rulings on each. *One commit*: the keystore gains
`RemoveSlotAndRotate`, `ChangeEntangledPasswordAndRotate`, `SetEntangledAndRotate` and
`ReplaceRecoveryAndRotate` over one internal rotation that `Rotate` now also calls, pruning and
wrapping the slot set the commit writes rather than the live one; FORMAT §8 already allowed it
and now says so. Two commits were declined: a stop between them leaves a removal without its
rotation that nothing on disk records, and the other order wraps the new VMK into the slot being
removed. *The retiring commit* (FORMAT R41): the losing superblock copy kept the whole state
before every commit, and a registry-only save left the other slot region copy as it was, so
after a removal the file itself still opened with the removed credential; every act that writes
the slot region is now followed by a full commit that writes the live region into the copy that
is not live, its rest zeroed, and re-seals the registry with only its date advanced, and every
region write zeroes the rest of its copy. The live copy is never written in place, which is what
FORMAT §4 doubles the region to avoid: a file that holds an old tail there takes a second
retiring commit once the first has made the other copy live. *No registry left unnamed* (the
second review's finding 1, taken as written): a registry no superblock copy names is not inert,
as the design first said — AES-GCM is a counter mode, and with the Metadata key it was sealed
under and one known block, every registry beginning with the same sixteen bytes, its holder
reads the rest — so every registry write pads its last 4 KiB unit with zeros, and every commit,
after its flip, writes zeros over every 4 KiB unit of the registry area that no superblock
copy's registry touches, up to the file's physical end, which the writer keeps, since a commit
that failed before its flip can leave a registry past the end of the last one: never a unit a
reader reads, never a superblock, so the zero-fill has no commit point. *Made durable* (ruled on
the first review's finding that a failed retirement was only logged): the mark is the file's own
bytes — a retirement is owed while either region copy holds anything but the live region or
zeros, the losing superblock copy is damaged, at another generation or names anything but the
live region, or the registry area holds a nonzero byte outside the two named registries — so it
survives a failure, a crash, a reopen and a copy, and the unlock finishes one before the Session
is installed. *Where it cannot be finished* (ruled 2026-10-27 on the second review, R-A): the
vault stays readable, and every mutation that writes a slot, an archive record or a secret, and
every backup, waits behind a barrier until the retirement has landed in full, each first trying
to finish it; the content of an existing archive does not wait, since a content write changes no
key and writes only the archive's receipt, which the design verified operation by operation, and
an archive's key, which no vault re-key changes, reaches content written before and after alike.
The barrier's state is read from the file's bytes and kept with the lock screen's facts, so it
holds across a lock and a Broken state; unknown outcomes stay Broken; a Tampered vault that owes
is retired as far as R25 allows — a registry-only commit in place of each retiring one, zeros
over the slot region copy no superblock copy names, the zero-fill — and refuses those mutations
only while a live copy's own tail remains, until a good copy is imported; a commit that fails
before its flip is retired at once inside its own act; an import over a vault that still owes
keeps a dated copy that still opens with what the owed change withdrew, which its confirmation
says. Revision 3's stricter reading, the vault kept locked, was ruled out, and so was its gate
on archive content. A superblock mark (`rotation_pending` reused) was declined: unauthenticated,
blind to files written before, and the bytes are the hazard itself. The first review's lighter
mirror-and-wipe was declined too: a second write path beside `commit`, and one that cannot clear
a tail in the live copy without writing it in place; its merits — no registry sealed, no date
moved, a shape the reader already accepts — were real. An earlier draft's reason, that two
bodies give the reader a fallback, was wrong and was struck: a reader chooses a superblock copy
by its checksum alone, and FORMAT has no fallback on a damaged body, which would be a rollback
lever. *One ceremony*: `Keys.RemoveSlot(id, rekey, backupPath)`,
`Keys.ChangeEntangledPassword(rekey, backupPath)`, `Keys.SetEntangled(on, rekey, backupPath)`
and `Keys.RotateNow(backupPath)` take the choice as arguments, their call IDs unchanged; the
user proves an unlock method once, is not asked for the YubiKey a re-keying removal takes away,
and a removal the invariant would refuse is refused before any ceremony; a lock that lands after
the commit ends the act done, not failed; a record that does not open is `vault.invalid`, not
`archive.invalid`. *The questions*: DESIGN §5's words, naming the method ("Why are you removing
this YubiKey?"), the re-keying answer first and pre-selected, each answer saying what it does; a
password, removed or changed, is asked whether someone else may know it; turning the password
on, whether the vault file could have been copied before; turning it off asks nothing. *The
limit*, said plainly under every question: "Re-keying protects the future, not the past:
whatever someone could open until now, they may already have copied.", with what the recovery
keys' escrow leaves open and the act that closes it; no dialog promises any longer that an
earlier copy "reaches no archive created from now on" while its recovery keys still open the
vault. The bodies of the password change and the re-key say that one unlock method proves it is
you and the other YubiKeys need not be present; they said no YubiKey was needed. The change's
disclosure says there is no field for the old password, while a YubiKey's proof still asks the
current one; it said the old password was not asked for. *The backup is taken after the change*
(ruled 2026-10-27, reversing 2026-09-07 and 2026-10-17, which took it before and ruled "no
backup means no rotation"), inside the act's ceremony, at the new key, by one rule in five
dialogs, ticked on every answer that re-keys, since the act's own re-key leaves every earlier
backup at the old key; and only once the retirement has landed, being otherwise skipped and said
so, its stamp unchanged: a backup taken before is at the key being retired, and adopting it
would bring that key back; the backups card now says when the last backup predates a re-key or
holds only replaced recovery keys, and the replace confirmation when a copy predates a re-key.
Writing the backup inside the ceremony also fixes *Back up, then re-key*, which, found by
reading the code, did not re-key: the page began the rotation while the export's ceremony still
ran, and the core refused it with `ceremony.in_progress`. *Replace recovery keys…* (ruled
2026-10-27; it was left undone on 2026-09-07, and the first review's option of folding it into
the removal was declined): an act of its own in the head of *Unlock methods*, one ceremony and
one commit — the proof, then as many new keys as the vault has, each with the old one's label,
shown in the recovery key's dialog until kept, then its one confirmation, which pairs each new
ID with the one it replaces, then the commit that adds them, removes every old recovery key and
re-keys, so no new key is ever escrowed where an exposed one reads it. No key's showing asks a
second time (R-B: 2026-09-09 holds everywhere, the replacement included; the design's first
reading of the quoted 2026-09-07 words, a second confirmation for a key written down, was
retired, and SCOPE and DESIGN were brought to 2026-09-09's words). A cancel or a failure before
the commit changes nothing, and the page names the keys shown and never added; an outcome not
known, a conflict included, which may come after the replacement committed, says to keep every
sheet until the Vault page's IDs, after a reopen, decide it, never to destroy them. Its end says
to destroy the replaced keys' sheets once the new backup opens, and of the earlier backups only
what is true: they still open with the replaced keys, and this vault can still import archives
from them (FORMAT §18.2). *The chain*: the removal's "lost" path, the password change's "someone
else may know it" path and a re-keying turn-on point onward in their info line and, as a button,
in their end, saying when it is needed — *Add an unlock method…* first where a YubiKey's removal
left the vault with recovery keys alone, then the password where it is on, then *Replace
recovery keys…*, whose own end offers the password where it is on and was not changed before it.
The end decides from what the act committed, carried in its final state, not from a slot list
read before. A chain is the acts begun from one another's ends: within one, no end points back
to keys or a password the chain itself changed; an act begun from the page starts a new chain,
so a later exposure gets the whole advice again (the second review's finding 6: the first design
suppressed it by every key made since the page loaded). Each act is its own commit, not one
repair: until the chain's last lands, the recovery keys an earlier copy held still open the
vault. *The recovery-key proof* (ruled 2026-10-27, narrowing 2026-10-17's "the dialogs offer no
recovery key"): a re-keying removal of the vault's only YubiKey proves with a recovery key, and
*Add an unlock method…* adding a YubiKey or a recovery key offers *Use recovery key instead*
under its wait and *Use recovery key* at its *No match*, the one exception APP §6's *No match*
rule now names; a password's removal still proves with the password, and nothing else does.
Cryptographically it adds nothing the VMK it yields does not give; through the app it lets a
recovery key enrol a YubiKey, and a showing is authorised by a protector, which may itself have
been enrolled with a recovery key, so the replacement and the recovery key's removal ask the
user to remove any unlock method they did not add. The case the feature exists for — one
YubiKey, lost, one recovery key — is its acceptance scenario: unlock with the recovery key, add
the new YubiKey with it, remove the lost one with a re-key, change the password if it is on,
replace the recovery key; with no new YubiKey yet, a second recovery key added the same way lets
the lost one be removed at once. *Finish setting up* is refused before its ceremony while the
vault's password is on (`vault.setup_password_on`), since a removal can now leave such a vault
with recovery keys alone and `AddFirstWayIn` cannot take its header; the lock screen greys it
and gives the accent to *Unlock with recovery key*. *Ends*: "Unlock method removed and vault
re-keyed", "Password changed and vault re-keyed", "Password turned on and vault re-keyed",
"Password changed", "Recovery keys replaced and vault re-keyed"; the change of the password has
its own ceremony kind, `password`, and the replacement `replace`. The remove dialog's body no
longer says a removed YubiKey or password still opens backups: a backup holds the recovery keys
only. Not done here: a *Change…* for the standalone password; a recovery-key proof for a vault
whose standalone password is forgotten; a lock-screen path that gives a vault left with recovery
keys alone, its password on, a YubiKey through `AddSlot`; erasing a shown recovery key's digits
from memory rather than dropping the last reference to them.
```

---

## 5. The build plan

Parts, in order. Every part runs its gates before it is called done. Go gates: `go build ./...
&& go vet ./... && go test -short -count=1 ./...` with `TMP` and `TEMP` (and `GOTMPDIR`) set to
D:/MyPersonalProjects/go-tmp, and for part B also `go test -race -count=1 -run
TestTheBarrierLeavesACeremonysHandleAlone ./internal/app/` (app test 38; the race detector needs
cgo and a C compiler). Frontend gates, in frontend/: `npm test`, `npx tsc --noEmit`, `npm run
check`, `npm run build`.

| Part | What | Files | Depends on |
| --- | --- | --- | --- |
| A. Keystore | 2.2: `rotate` with `adds`, `without`, `RemoveSlotAndRotate`, `ChangeEntangledPasswordAndRotate`, `SetEntangledAndRotate`, `PreparedRecovery`, `PrepareRecovery`, `ReplaceRecoveryAndRotate`, `Retire` (`Unlocked` and `Session`, with its Tampered branch and `zeroOther`), `RetireOwed`, `RetireRemnantOnly`; `Rotate` and `RemoveSlot` through them; `recoveryRecord` split out of `newRecord`; in `commit` the zero-filled region write, the zero-padded registry write (`regPadEnd`), `txn.retire` (the other copy only), the carried retirement, the scrub after the flip with its check of the disk, the physical end `k.end` (rule 8) and `k.owed` with its `retireState` (computed by `readRetire` in `load` and `create`, set before a commit's first write, computed again after every flip); `PickKeystoreSuperblock` returning the loser's decode; the `commitFault` and `writeSeen` seams and their exported setters `SetCommitFault` and `SetWriteSeen`; keystore tests 1-34 of 2.6 | internal/keystore/mutate.go, internal/keystore/slots.go (`recoveryRecord` only), internal/keystore/file.go, internal/keystore/unlock.go (`Session.Retire`), internal/format/pick.go and keystore_superblock.go (the loser's decode), internal/keystore/keystore_test.go, internal/format tests for `pick` | nothing |
| B. App and API | 2.3-2.7: the six acts, `actOpts`/`beginAs`, `landChange`, the proof's `without` and `recoveryProof`, `backUpAfter` with its admission, `backupPlaceOK`, `vaultErr`, `recoveryHex`, `keep` and `confirm`, `finishRetireLocked` and `publishUnlockedLocked`'s error, the codes `vault.retire_owed` and `vault.setup_password_on`, the barrier (`retireBarrierLocked`; `publishRegistry*` at the held writes and the barrier at their early checks; `beginAs`'s call with `reads` and the Delete claim; the purge's skip; *Finish setting up*'s retirement), the cached `retireOwed`, `AfterFacts`, `FinishSetup`'s refusal, the replacement's dropped handles, the seams, the `CeremonyState` fields and steps, `BackupStatus`, `FileInfo`, `lastExport` and `EnrollOptions` fields, the cached generation and `retireFailed`, kinds "password" and "replace"; `landChange`'s at-once retirement of a failed commit and its Done after a landed one; `RetireRemnant`; `VaultStatus.RetireOwed`; `InspectRecords` begun without `reads`; app tests 1-40; the call sites; the shell's comment on the raw channel | internal/app/keys.go, internal/app/ceremony.go (the prompt kinds' comment), internal/app/types.go, internal/app/settings.go, internal/app/session.go, internal/app/vaultfile.go (`InspectFile`; `FinishSetup`'s refusal and its call of `publishUnlockedLocked`), internal/app/archives.go (the `seams` fields), internal/app/ops.go (`CreateArchive`, `RotateKey`), internal/app/records.go (`ForgetArchive`, `RestoreArchive`, `DeleteArchive`, `publishRegistryAt`), internal/app/merge.go (`MergeRecords`, `InspectRecords`), internal/app/errors.go (two codes), internal/app/api/api.go, internal/app/api/api_test.go, internal/app/rekey_test.go (new), internal/app/core_test.go, internal/app/entangle_test.go, internal/app/mutation_test.go, internal/app/merge_test.go, main_windows.go (one comment) | A |
| C. Bindings and mock | `wails3 generate bindings -clean=true -ts -i` from the repo root (build/Taskfile.yml:61); the mock of 3.10 | frontend/bindings/** (generated), tools/uimock/server.py | B (the bindings are read from the compiled signatures) |
| D. Frontend | 3.1-3.11 and the tests of 3.9 | 3.11's list | C |
| E. Spec | section 4 | docs/APP.md, docs/SCOPE.md, docs/DESIGN.md, docs/FORMAT.md, docs/DECISIONS.md | nothing to write; lands with or after D |

What can run in parallel (disjoint files):

- A and E: E is prose and touches no code. E lands in the same commit as the code or after it, so
  that the docs never describe what is not built.
- The mock half of C can be drafted beside A and B (the IDs are known, 2.1), and D can be drafted
  beside A and B against the signatures of 2.1 and 2.5; neither can pass its gates before C's
  bindings exist.

What must be sequential: A before B (B calls A's methods); B before C (the generator reads the
compiled Go); C and D land together or D right after C: the regenerated keys.ts changes the
signatures every call site uses, so `npx tsc --noEmit` fails between them.

A can be split if a builder wants smaller reviews: A1, the retirement and the scrub alone
(`commit`, `Retire`, `RetireOwed`, the registry padding, the scrub, `k.end`, the loser's decode,
the seams, tests 14-20, 22-31 and 33) — which changes nothing a user sees until B calls it, though
the carried retirement and the scrub (2.2.3, rules 4 and 6) then work for every registry save at
once — and A2, the combined commits and the replacement (tests 1-13, 21 and 32). B can likewise
land the barrier (2.4, app tests 10, 11, 29-35 and 38) before the acts that need it.

Coordination with the other agent: D touches no menu component and not app.css (3.11), but its
frontend gates build into frontend/dist and must not run while that agent's builds run; E's
docs/APP.md and docs/DECISIONS.md are files that agent also commits (b79a10c touched both), so E
is applied on its latest commit, by quoted text rather than by line number.

Commits: A and B can be one commit or two; C and D one; E alone or with D. Each ends with the
attribution the session asks for. None amends or rewrites history.

---

## 6. Open questions, follow-ups, and what could not be confirmed

### Open questions for the owner

None. The two revision 3 put to the owner are ruled (R-A and R-B of 2026-10-27, applied in 2.3,
2.4, 2.7, 3.3 and section 4), and nothing else in this design waits on a choice a builder cannot
make from its text. Five choices are the designer's, made here with their reasons so that the
owner can weigh them again without anything else moving: the full second commit over the mirror
and wipe (2.2.3); the scrub's unit, 4 KiB, which FORMAT already calls the atomic-write unit
(2.2.3, rule 6); the barrier's rule for what it holds — a record added, removed or forgotten, a
secret, a backup — which leaves an archive's name, description, hidden flag and path and the
vault's lock times outside it (2.4; one call each to move inside); the Tampered vault's repair, a
registry-only commit and zeros over the unnamed region copy in place of a retiring commit, which
critique 3 left to the owner under R-C's "import or repair" (2.2.3; 7.7, finding 3); and the
recovery-key switch for adding a recovery key as well as a YubiKey, which ruling 3's words allow
and critique 3 left to the owner (2.3; 7.7, M6). Two widenings of ruling 3 stay
follow-ups (1 and 2 below), the owner's to reopen.

### Follow-ups, outside this change

1. **A vault left with recovery keys alone while its password is on has no lock-screen path to a
   YubiKey.** Removing the only YubiKey (allowed with two recovery keys, slots.go:189-196) makes
   the vault *setup needed* (vaultfile.go:200-210); `AddFirstWayIn` (vaultfile.go:614) refuses
   a header whose password is on (mutate.go:245-247). This change refuses *Finish setting up*
   before its ceremony there, greys it on the lock screen and gives the accent to *Unlock with
   recovery key* (2.3, 3.8), after which *Add an unlock method…* on the Vault page works, by
   `AddSlot`. A lock-screen enrolment through `AddSlot` for an already entangled header would
   remove the detour; it is not this feature's.
2. **A forgotten standalone password is the same dead end as a lost YubiKey.** A vault of a
   password and a recovery key: *Remove…* is greyed by the invariant, and *Add an unlock
   method…* proves with the password (keys.go:325). Ruling 3 reopened the recovery-key proof for
   a lost YubiKey only ("never otherwise"); the switch of 3.4 would serve here unchanged, drawn on
   the typed proof's view.
3. A *Change…* for the standalone password: today it is changed by adding a password and removing
   the old one, which the page does not guide.
4. The on-screen words for the invariant overstate the keystore's rule: `vault.invariant` says "At
   least 2 unlock methods are always kept, one of them a recovery key." (strings.ts:235) and
   `vault.no_recovery_slot` "A vault always keeps a recovery key." (strings.ts:238), while the
   invariant asks only for two methods with disjoint secrets (slots.go:155-197), so two YubiKeys
   with the password off can lose the last recovery key. This design stops repeating the claim in
   new text, says where no backup can be made, draws the info lines' recovery-key sentence only
   where a recovery key exists, and words the greyed replacement's reason on its own.
5. The CSS this design copies into components (3.11) moves to app.css once the other agent is done
   with it.
6. Re-keying every archive in one act, the third row of DESIGN §5's table: every dialog's *How
   this works* now says an archive that existed when an earlier copy was opened stays open to it
   until re-keyed on its page; a vault-wide act would make that a step like the replacement, and a
   single compromise-repair act — the removal, the password, the recovery keys and the archives
   in one ceremony — would close the gap between the chain's acts that 3.3 (i) states.
7. The ordinary showing's lead says "With this key, the vault file or a backup opens without your
   YubiKey." (`revealCopy.leadOpens`, strings.ts:2121), which is wrong on a vault with no
   YubiKey; the keep mode no longer inherits it (3.3), and the ordinary showing's own words are a
   small fix outside this change.
8. `vault.setup_needed`'s copy, "This vault has only its recovery key so far. Finish setting it up
   first." (strings.ts:229), is wrong where a removal left two recovery keys. This change words
   the replacement's greyed reason and the Vault page's line on their own (3.3, 3.7); the code's
   copy, shown wherever else the core refuses with it, is worth the same care.
9. **A shown recovery key's digits are dropped, not erased.** Dropping a handle removes the
   preview server's entry (preview.go:185-190) and the page's reference; the digits remain in an
   immutable Go string (`mintSecret`, preview.go:164-170) and a JavaScript string
   (RecoveryReveal.svelte:53) until the collectors reuse that memory, for every showing today and
   for the replacement's (the outside review's second reading, its Q2 position). Holding them in a
   locked page on the Go side and in a typed array the page zeroes would change the showing as a
   whole, which is not this feature.

### What could not be confirmed

- Nothing was run: not the app, the mock or a test. The retirement's predicate rests on reading
  `load`, `commit`, `registryTarget`, the trim and `updateRegistryAt` (file.go:103-142, 298-452;
  unlock.go:386-399), `pick` (pick.go:66-83) and `create` (file.go:458-560, which writes slot
  region A only, so copy B of a new file reads as zeros: the file is extended by the registry's
  write at 0x42000, and a file's unwritten bytes below its end read as zeros); keystore tests
  14-33 settle it. The *Back up, then re-key* finding is from reading the code, and the outside
  review confirmed it the same way.
- **The known-plaintext recovery of leftover registry ciphertext** (finding 1) is the outside
  review's inference from the codec and this design's reading of AES-GCM (file.go:226-231;
  format/registry.go:387-393); it was not executed. Keystore tests 26 to 28 carry a control that
  must recover a wrapped archive key from a remnant before the same test shows the scrub leaves
  none; if a control fails to recover it, the attacker helper is wrong, not the scrub right.
- **The scrub's unit.** It writes whole 4 KiB units no named registry touches, on FORMAT's ruling
  that 4 KiB is the atomic-write and alignment unit (FORMAT.md:1728-1732); a device whose atomic
  unit is larger (some flash translation layers) was not considered there and is not here. The
  hazard of writing the live slot region copy in place rests on the region's end not being
  sector-aligned and on `load` having no body fallback (both read, file.go:103-142;
  FORMAT.md:839-842, 950-960); whether a given drive tears or loses a sector on power loss was not
  tested. The design avoids both writes either way.
- Where `registryTarget` puts a retiring commit's registry (file.go:447-452) decides which of the
  placement table's rows applies (2.2.3); reasoned from the placement rule and the trim, not run.
  Keystore tests 26, 27 and 29 cover the rows.
- **The table of 2.4** was built from the fifteen registry writes and the six receipt sites a
  search for the four update entry points and `recordReceiptLocked` finds outside the tests, each
  read at b79a10c; a write reached by another path would not be in it, and none was found. That
  the archive's own superblock records the commit's time, which the receipt takes from the clock
  (archive/tx.go:767, 889), was not traced; the receipt's other fields are read from the file.
- **The physical end** (`k.end`, 2.2.3 rule 8) is reasoned from file.go:104-108 and 389-452 and
  from critique 3's sequence, not run; keystore test 28's three placements and its failed-trim
  run are built to show it. Whether NTFS keeps a truncation that no fsync followed, so that a power
  loss after the trim could bring a cut tail back, was not checked; `load`'s stat and the fourth
  clause would count such a tail either way.
- **The Tampered branch's zeros** over the unnamed region copy rest on the layout
  (FORMAT.md:839-842: both copies 4 KiB aligned and 128 KiB long) and on both superblock copies
  naming the live copy when the write is made, which `zeroOther` checks first; a torn 128 KiB write
  was not tested on any drive, and it leaves the copy owed by clause 2 while harming nothing a
  reader reads.
- The exported seams `SetCommitFault` and `SetWriteSeen` are the first test hooks internal/keystore
  exports (a search for an exported setter or a `ForTest` name in internal/ found none); the app's
  tests named `commitFault` from revision 3 on, which their package could not set. App test 38's
  `-race` run was not made.
- Whether Chromium's implicit submission fires on Enter in a radio is taken from APP.md:3193-3195,
  which says the form dialogs rely on it; not tried here.
- The dialogs' heights, the ceremony end's heights (3.6) and the three head buttons' widths are
  estimated from app.css (460 px box, 24 px body inset, 13/20 body type, 8 px bar padding, the 52
  px disc; the 760 px column), not measured; the export's time to a USB drive, which the "Saving
  the backup…" lead covers, is not measured.
- How often the idle lock ends a replacement while a key is written by hand was not measured;
  the ready line names the idle time and the keep view offers *Stay unlocked* in its last
  minute (3.3).
- The fake card layer's support for two cards in one test (app tests 25 and 26) was read
  (fakes_test.go:215-575: `setCard` swaps the card behind the reader) but not exercised.
- `tokenCredentialFor`'s refusal of a YubiKey that matches no listed slot was read
  (ceremony.go:846-855), which is why the proof keeps every slot and only the wait's line leaves
  the removed one out (2.3); how that refusal reads on screen was not checked.
- SYNC.md: slots are not synced (SYNC.md:18), and a device rotates on a removal with no peer
  needing to know (40-41), so the replacement and the retirement are one device's acts; the
  combined commits re-wrap the identity key exactly as `Rotate` does (mutate.go:642-655), and the
  retiring commit and the scrub change no registry field a peer reads.
- The second review's 44 files were not re-read beyond the anchors it names. Its anchors were
  spot-checked at b79a10c and hold: keys.go:207-212 and 236-242 (`writesRegistry` lists save,
  verify, compact and rotate), ops.go:597 and 1095 (the content operations register as "add" and
  "replace"), ops.go:2104 and 2223 (the re-key's and the create's registry writes),
  session.go:596 (a lock clears the Session and the handle), strings.ts:1579 and APP.md:3741-3742
  (the old password "is not asked"), APP.md:1662-1664 (the *No match* rule), KeysPage.svelte:77-87
  and 328-335 (the slot reads), state.svelte.ts:1006 and 1036 (the store's reads at a change of
  state).

---

## 7. Against the outside review and the critiques

The review (scratchpad astra_vmk_design_review.txt) read revision 1 whole with 36 files; its
verdict was "revise before building". Each position and finding, with what this design does
(7.1-7.4); then the two critiques of revision 2 (7.5); then the second outside review, of
revision 3, and the owner's rulings on it (7.6); then the two critiques of revision 4 (7.7).

### 7.1 The five positions

1. **Q1, the backup's order: after the change and its retirement.** Taken, by ruling 1. The
   partial success it asks to be reported is `BackupError` under the end's title (2.5, 3.6); the
   reversal is recorded in FORMAT §18.3, APP.md §13 and DECISIONS (4.1, 4.4, 4.5); the defect it
   confirmed (*Back up, then re-key*) is fixed by the backup moving inside the act (3.2).
2. **Q2, replacing the recovery keys atomically in the 1.0 compromise flow.** Taken in another
   form, by ruling 2: not folded into the removal or the password change, but an act of its own,
   *Replace recovery keys…*, which stages and confirms the new keys and then replaces the whole
   set and rotates in one transaction (2.7) — the review's own shape, reached from the page head
   or from the end of the acts that need it. The review's invariant point — a vault of recovery
   keys alone needs two fresh ones, not one — holds by construction (as many new keys as old,
   2.2.1), and such a vault is refused the act anyway, since no protector would remain to show a
   new key again (2.3). "Include changing exposed entangled-password material": taken as a
   pointer, not folded — the replacement's dialog and the removal's and the change's info lines
   and ends say to change the password too (3.1, 3.3), the password change being its own act with
   its own question.
3. **Q3, the retiring commit: keep the full second commit, but not best effort.** Taken. The full
   second commit stays, for its reuse of `commit` and because it clears an old tail in the live
   copy by a flip, never by a write in place (2.2.3); revision 2's third reason, a fallback
   through two bodies, was wrong and is struck (7.5, F3). Best effort is replaced by a retirement
   owed by the file's own bytes — the two region copies and the losing superblock, and since
   revision 4 the registry area — carried by every registry write, finished at unlock, and since
   revision 4 holding every mutation that writes a record or a secret while it stands (7.6). The
   review's lighter mirror-and-wipe is weighed and declined (2.2.3), as the review allowed ("not an
   unequivocally stronger replacement"); its point that retirement does not establish physical
   erasure or reach old copies is in R41's text.
4. **Q4, one transaction for the change and its rotation.** Taken, unchanged from revision 1, and
   extended to the replacement.
5. **Q5, the framing.** Taken: every "reaches no archive created from now on" is qualified by what
   the recovery keys' escrow still reaches (3.1 item 7, 3.2, DESIGN's table, 4.3); the Turn on
   question keeps its uncertain answer as the default, now with the reason said (3.2); the
   lost-YubiKey path is the acceptance scenario (3.4; app test 26).

### 7.2 The eight findings

1. **[high] Retained recovery keys defeat the promised revocation.** Taken: the unconditional
   claims are gone from the dialogs, DESIGN's table and its TRIM paragraph (3.1, 3.2, 4.3), and the
   atomic replacement exists (2.7, 3.3); keystore test 21 asserts both halves.
2. **[high] Retirement can fail while the ceremony reports Done.** Taken in another form. A
   distinct result: the act still ends Done — the change is true, and a failed state would offer
   *Start again* for a change that landed — with `RetireError` under its title saying what is
   not yet true, and the standing warning `vault.retire_owed` (2.4, 2.5, 3.6). Durable: the bytes
   are the mark (2.2.3). Resumed before revocation is relied on and before writes: every registry
   write carries a pass, every mutation that writes a record or a secret waits for it in full
   (revision 4, R-A; it was archive writes in revision 3), and the unlock finishes it before the
   Session (2.2.3, 2.4). Broken kept for unknown outcomes. The crash table's false row is
   corrected (2.2.3). The fault tests it names are keystore 16-18 and 22-24 (before and during the
   writes, both fsyncs, a lost superblock write, a torn write, across a reopen, after a
   registry-only save, the raw abandoned bytes inspected) and app 9-12; the confirmed fact under
   it — a registry-only save does not rewrite the slot region (unlock.go:398, file.go:345) — is
   section 1, item 6, and is what the carried retirement answers.
3. **[medium] The common lost-YubiKey vault cannot enrol its replacement.** Taken, by ruling 3: the
   recovery-key proof at *Add an unlock method…*, reached by *Use recovery key instead* on its
   wait (2.3, 3.4); the 2026-10-17 ruling is amended in APP.md and DECISIONS (4.1, 4.5); the reveal
   keeps its protector rule.
4. **[medium] The instructions omit the exposed entangled password.** Taken: the info lines and the
   ends name it where the password is on (3.1, 3.3, 3.6), and R38 and §15 say the change rotates
   too (4.4); keystore test 21 includes the change.
5. **[medium] Dialogs promise no YubiKey is needed while requiring one.** Taken as written: the
   password change's and the Re-key's bodies, and APP.md's (3.2, 4.1).
6. **[low] The mock keeps an immediate toggle for Turn on.** Taken as written: `SetEntangled` goes
   through the ceremony model with its arguments, and scenes play its phases and results (3.10).
7. **[low] The backup warning includes backups made before the key was enrolled.** Taken as
   written: "Backups made while {label} was an unlock method…" (3.1, 3.7).
8. **[low] The APP replacement boundary duplicates the sentence before it.** Taken as written: the
   replacement starts at "**Rotation re-derives the Session:**" (4.1).

### 7.3 Its gaps

- APP.md:2554's refresh triggers: taken (4.1, the backups card).
- Retirement fault injection, and a lock between the two commits: taken (keystore 16-18; app 9).
- The backup helper's admission: taken (2.3: the mutex released before `cer.set` and
  `recordExport`; an export admitted before a lock runs to its end and is recorded).
- What it could not verify (Chromium's Enter, focus announcements, sizes, hardware, latency,
  physical erasure) stays unverified here too (section 6).

### 7.4 The earlier critiques, where this revision changes their treatment

Revision 1's section 7 (scratchpad design.md) stands for every item not named here.

- UX 4's switch, "Use recovery key instead" on the act's wait: it was taken in another form (the
  core choosing the recovery key only where nothing else proves); it is now taken as proposed for
  *Add an unlock method…*, by ruling 3, and still not for any other act.
- UX 6's "Backup saved…" after a failed or cancelled act: moot, since no backup is written before
  the change (ruling 1); its "Nothing was changed." stays.
- Revision 1's Q1 and Q2 are answered by rulings 1 and 2; the "keep it before" variant and the
  recommendation to leave the replacement undone are gone.
- F4's third answer, re-keying at *Finish setting up* of an adopted backup: still not taken; the
  backups card's two flags (`VaultRekeyed`, `KeysReplaced`) ask for a new backup instead.

### 7.5 The critiques of revision 2

Two critics read revision 2 whole: one for format and security correctness
(scratchpad/vmk-rotation/critique2-format.md, findings F1-F14), one for the user's flows and the
specs' consistency (critique2-ux.md, findings 1-19). Both found it sound with changes. Every
finding at minor or above is taken; none was found wrong or against a ruling. Where one is taken
in another form or leaves a choice to the owner, it says so. Where an answer below put a question
to the owner (F4, UX 6) or kept a claim revision 4 withdraws (F14's inert extent), the owner has
since ruled or the claim is struck, as the entry says and 7.6 explains.

The format critique:

- **F1 (major), the predicate missed the losing superblock.** Taken: a third clause (2.2.3,
  R41), `PickKeystoreSuperblock` returning the loser's decode, test 16's "owed after superblock"
  now true by it and asserting that the next retirement overwrites that copy, and test 23. Taken
  one step further than asked: a losing copy that fails its checksum is owed too, since a torn
  superblock write can keep the old offset and nonce in its unwritten part.
- **F2 (major), the live copy written in place.** Taken as written: the retiring commit writes
  only the copy that is not live, `Retire` makes at most two, and test 19 asserts two commits and
  no write to the live copy (2.2.3, R41).
- **F3 (major), a reader fallback FORMAT does not have.** Taken: the claim is struck from 2.2.3,
  the crash table, R41, 7.1 and DECISIONS, and R41 says what a reader does. The full second commit
  is kept on its other reasons (the reuse of `commit`, and the live tail it clears by a flip,
  which the mirror and wipe cannot without writing in place, F2); the mirror and wipe's merits are
  stated so that the owner can weigh them again (section 6).
- **F4 (major), archive writes admitted while a failed retirement is owed.** Taken in revision 3
  by a gate at `beginOp`, with the stricter reading, the vault kept locked, put to the owner.
  Ruled since (R-A): the vault stays readable; the barrier holds the keystore and registry
  mutations that write a record or a secret, and a backup, and not the content of an existing
  archive, which writes neither (the verification is 2.4's table); `beginOp` has no gate (2.4,
  7.6).
- **F5 (minor), the handle's flag after a failed commit.** Taken as written (2.2.3 rule 5, which
  revision 4 widens to any write; test 14).
- **F6 (minor), the torn write.** Taken as written: the crash table's wording, test 22, and the
  raw-bytes search for the record's `epk` and `wrapped_vmk` as well as its ID (2.6).
- **F7 (minor), what the recovery-key proof adds through the app.** Taken as written: 2.3's and
  DECISIONS' wording, "remove any unlock method you did not add" in the replacement's when-line
  and the recovery key's removal (3.1, 3.3), and test 21's thief's YubiKey.
- **F8 (minor), a third recovery-key case.** Taken by the first fix offered: `without` is set for
  a YubiKey only, so a password's removal proves with the password (2.3; app test 27).
- **F9 (minor), three FORMAT sentences.** Taken as written: R25, R28 and R38 deltas (4.4).
- **F10 (minor), the end offering a refused act.** Taken, merged with UX 2: the "add" follow-up
  (3.6).
- **F11 (minor), minted handles never dropped.** Taken as written: the act's deferred cleanup
  (2.7; app test 21).
- **F12 (nit), §8's step 6.** Taken: the retirement follows the atomicity paragraph, unnumbered
  (4.4).
- **F13 (nit), "retire" already means three things.** Taken in its second form: R41 defines its
  word against R31's retired copy and `slot_state` 2. The word itself is kept: the owner's ruling
  4 says "retirement", "retiring" and "changed but not retired", and the code's names follow it.
- **F14 (nit), four small points.** All taken: the prepared IDs checked against each other, the
  prepared keys in a locked page, "re-sealed with only its date advanced" (the reason revision 3
  gave beside it, that an unnamed registry extent is inert, is struck in revision 4: 2.2.3; 7.6,
  finding 1), and the defer narrowed so that it never reaches `*kpNew` (2.2.1).

The UX critique:

- **1 (major), the backup tick against the backups card.** Taken in its first form: every answer
  that re-keys ticks the backup whenever a recovery key remains, with the re-key's own reason, and
  the tick follows the answer (3.1 item 6, 3.5). The second form, dropping `VaultRekeyed` from
  *behind*, was not taken: a backup at a retired key brings that key back when adopted, which on
  the "lost" path is the hazard itself.
- **2 (major), ruling 3's first case dead-ends.** Taken as written: the "add" follow-up, the
  replacement's greyed reason in words of its own, `setupLine` pluralised, and the tests (3.3,
  3.5, 3.6, 3.7, 3.9).
- **3 (major), *Finish setting up* on such a vault.** Taken as written: refused before its
  ceremony with `vault.setup_password_on`, greyed on the lock screen with the accent moved to
  *Unlock with recovery key*, 3b reworded, section 1 and follow-up 1 corrected (2.3, 3.1, 3.8;
  app test 28).
- **4 (major), the scenario's loop.** Taken: one chain, the password first, the replacement last
  with no follow-up, and the scenario and app test 26 reordered (3.3 (i), 3.4, 3.6). The
  suppression is by the IDs a replacement made since the page loaded rather than by
  `SlotView.CreatedAt` against the removal's time: it answers the same case, needs no clock, and
  also covers a change made after a replacement begun from the head. (Revision 4 scopes it to a
  chain of acts begun from one another's ends instead, since page-lifetime IDs outlived a lock
  and a later compromise, and lets the replacement's end offer *Change…* where the password was
  not changed in its chain: 7.6, finding 6.)
- **5 (major), "destroy the backups made before".** Taken as written: the end and the recovery
  key's *How* say what is true of those backups and leave them to the user (3.1, 3.3).
- **6 (major), ruling 2's showing rule overwritten.** Taken in revision 3 by putting the question
  to the owner. Ruled since (R-B): the critique's option (a), 2026-09-09 everywhere with the
  replacement's one confirmation, and SCOPE and DESIGN brought to its words (2.7, 3.3, 4.2, 4.3).
- **7 (minor), the end on the touch's green.** Taken as written, with the heights estimated again
  for the neutral view (3.6).
- **8 (minor), the recovery notes by kind.** Taken as written: `recoveryNoteOf` (3.4, 3.5).
- **9 (minor), the keep mode's failure words.** Taken as written (3.3 (d) 2).
- **10 (minor), the idle lock while writing.** Taken as written: the ready line names the idle
  time, and the keep view carries *Stay unlocked* in the last minute (3.3).
- **11 (minor), the replacement's copy.** Taken: (a) `when` by count; (b) `howOn`; (c) the
  method the copy was opened with, in `when`; (d) `recoveryOn` reworded (3.3, 3.7).
- **12 (minor), the turn-on's follow-up and a vault with no recovery key.** Taken as written (3.1
  item 5, 3.2, 3.6).
- **13 (minor), the switch at *No match*.** Taken as written: the step's body and the accent *Use
  recovery key* through `dialogFoot` (3.4, 3.6).
- **14 (minor), the spec deltas' wording.** Taken: (a) SCOPE's sentence is no longer rewritten
  (UX 6); (b) the done line's count; (c) the Vault page's insert; (d) the replacement's dialog in
  APP §13's "Once … is answered"; (e) "no standalone password"; (f) the Re-key body and the keep
  lead made true on a vault with no YubiKey (4.1, 3.2, 3.3).
- **15-19 (nits).** All taken: the lock screen's at-rest button and the step that opens on the
  lost YubiKey's wait (3.4); no sentence ending on a command's "…" (3.7); one row per key at the
  confirmation and in the receipt (3.3); the retirement's bar says what a copy opens with and what
  the user does that finishes it (3.6, 3.7); the next-step line says "when it was lost", and the
  switch has a closure of its own with the label (3.4).

### 7.6 The outside review of revision 3

The review (scratchpad astra_vmk_design_review2.txt) read revision 3 whole with 44 files; its
verdict was "revise before building", keeping the atomic replacement and the combined commits. Its
anchors were spot-checked at b79a10c and hold where this section relies on them (section 6, What
could not be confirmed). The owner ruled on it the same day (R-A to R-D, at the head of this
document). Each position and finding below is *taken* (as the review asks), *taken in another
form* (the defect closed, another way), or *answered by a ruling*.

**The five positions.**

1. **Q1, a retirement that cannot be finished.** The review: keep the vault readable, with a
   strict barrier against vault and archive mutations, Broken for unknown outcomes, and the backup
   after the change and a completed retirement. *Answered by R-A, in another form for the
   archives*: the vault stays readable; the barrier holds every keystore and registry mutation R-A
   names until the retirement has landed in full, each first trying to finish it (2.4); unknown
   outcomes stay Broken; the backup waits for a completed retirement (R-C, finding 8; 2.3). The
   content of an existing archive is not held, by the ruling, and the verification the ruling
   asks for is done (2.4's table; finding 2 below). The review's point that staying locked would
   not stop open archives, which stay writable across a lock (APP.md:624), is why neither reading
   was a matter of the Session alone; under R-A it needs no answer, since content is not held.
2. **Q2, the recovery keys.** Keep the separate atomic replacement: *kept* (R-D). Deferred cleanup
   frees handles but does not erase immutable strings: *taken*, said in 2.7 step 4 and section 6
   (follow-up 9), not fixed here, since it holds for every showing. 2026-09-09 everywhere with the
   replacement's one confirmation, and SCOPE and DESIGN reconciled: *answered by R-B*, applied in
   2.7, 3.3, 4.1, 4.2 and 4.3.
3. **Q3, the retiring commit.** Keep the full retiring commit but repair its guarantee: *taken*.
   The predicate gains the registry area (clause 4), every commit scrubs, and the inert claim is
   gone (finding 1); a legacy file is retired in full before anything is published (finding 3);
   the costs it lists — a 128 KiB region write, the registry, the superblock, two fsyncs, a read
   of about 260 KiB, two passes for a legacy tail — are stated in 2.2.3, now with the scrub, and
   it confirms they are not costs of a steady-state save, which the scrub keeps true (a save that
   leaves nothing unnamed writes nothing more). The mirror-and-wipe stays declined, on 2.2.3's
   reasons, which the review weighs the same way. The missing tests are added (2.6, keystore 25-31
   and app 29-37).
4. **Q4, one commit.** Keep one commit for each change and its rotation, the replacement
   included: *kept* (R-D). "Do not describe revocation as complete merely because the first flip
   landed": *taken*. FORMAT §8's added paragraph says the retirement is what removes the
   predecessor's accessible remnants, R41 says what is owed until it lands, and an act whose
   retirement failed ends Done with a bar under its title saying what the file still opens with
   (3.6), the title naming the change and not claiming the revocation complete.
5. **Q5, the framing.** The lost-YubiKey walkthrough is reachable: *confirmed*, unchanged (3.4).
   The reopened proof is an expansion of what the app lets a recovery key do, not a cryptographic
   authorization boundary: *taken* — 2.3 says so in those terms, as revision 3 began to (critique
   2, F7), and DECISIONS keeps it. The separate acts are not one compromise-repair transaction:
   *taken* — 3.3 (i), APP §13 and DECISIONS say that until the chain's last act lands the recovery
   keys an earlier copy held still open the vault, and whoever holds one reads what it holds
   meanwhile; a single act is follow-up 6.

**The eleven findings.**

1. **[high] Erasing the nonce does not retire leftover registry ciphertext.** *Taken as written*
   (R-C). The claim that unnamed registry ciphertext is inert is removed from 2.2.3, R41 and 7.5's
   F14 note, and FORMAT §4 and R41 now say why it is not (AES-GCM's counter mode, the known first
   block). The cleanup: every registry write pads its last 4 KiB unit with zeros; every commit,
   after its flip and trim, zero-fills the 4 KiB units of the registry area no named registry
   touches, never one it touches and never a superblock, so never a live sector and never a commit
   point (2.2.3, rules 2 and 6); the retiring commit's placement is reasoned case by case, and in
   each the pre-change registry is overwritten, trimmed or zeroed once the retiring flip lands
   (2.2.3's placement table); the owed condition gains clause 4, so that a zero-fill cut short or
   failed, and a registry write that failed after writing, are finished by the next gated mutation
   or unlock, by a scrub with no commit. The adversarial fixtures it asks for are keystore tests
   26 (shrink), 27 (growth) and 28 (failed write), each with a control that recovers the key
   before the fix, plus 29 (no scrub write on a named unit), 30 (an interrupted scrub) and 31 (a
   legacy registry's slack).
2. **[high] An admitted archive operation can cross a failed retirement.** *Answered by R-A, with
   the verification it asks for.* Archive content writes are not held, so an add admitted before a
   removal and finishing after a failed retirement writes content that is exposed exactly as content
   written after the retirement would be: the archive's key is not changed by a VMK rotation
   (FORMAT.md:1278-1280), and the limit every dialog states covers it. The verification (2.4's
   table): every registry write goes through `updateRegistryAtLocked` (session.go:398-442), whose
   fifteen callers and the receipt's six were read; each content operation — add, replace
   (ops.go:597, 1095), new folder, delete, rename and move inside an archive (archives.go:1627,
   1676, 1732, 1824), compaction and the reclaim — writes only the receipt (ops.go:1542-1569), whose
   fields describe the archive file just written and are neither a record nor a secret. So no
   content operation is gated, and the review's admission race no longer bears on a guarantee. What
   remains of the finding is its last point, a `Session.Retire` racing a ceremony's use of the
   keystore: *taken* — the barrier refuses `ceremony.in_progress` while a mutation ceremony or a
   pending touch holds the handle, as `updateRegistryAtLocked` does, and `beginAs` calls the barrier
   under the same hold of the state mutex as the ceremony's creation, so the two never overlap (2.4;
   revision 5 puts that check first, so that the barrier never reads such a handle either, 7.7,
   finding 4). The add paused after admission is not tested, being no longer a hazard; app test 29
   shows content landing while gated mutations are refused.
3. **[high] One carried pass can publish new secrets while a legacy retirement remains owed.**
   *Taken as written* (R-C). The barrier finishes the retirement in full — `Retire` loops until
   nothing is owed, two retiring commits and their scrubs for a legacy file — before any gated
   registry write, and does so inside `publishRegistry*`, before the write's `modified_at` is
   computed; `CreateArchive` and `RotateKey` meet it at their early checks and at their writes,
   and proceed only with nothing owed, never because a pass committed (2.4). The carried pass of
   ordinary saves stays, as progress, is said not to be what holds publications back, and falls
   back to a plain save when it cannot be written, so that a write the barrier does not hold
   never fails for the retirement's sake (2.2.3, rule 4). The combined test it asks for is app
   test 30 (a legacy tail, the unlock's retirement failed before its first pass, a fresh archive
   key's publication attempted and refused, then two passes landing before it, the raw file read
   after every commit).
4. **[high] Tampered state bypasses the barrier.** *Taken as written* (R-C). A Tampered vault that
   owes a retirement stays readable, and every mutation R-A names is refused — the keystore acts
   by `vault.tampered` as today, the registry mutations and the export by the barrier with
   `vault.tampered` — until an import establishes a clean state; R25 still forbids copying the
   damaged region forward, so no retiring commit is made and no save carries one, while the scrub,
   which writes no region, runs (2.4). Revision 3's exception is struck from 2.4, APP §2.1 and R41;
   keystore test 18 is amended and app test 31 added; a Tampered vault that owes nothing behaves as
   today. Revision 5 makes, beside the import, the repair R-C names: everything that copies nothing
   forward, so that only a live copy's own tail is refused (7.7, finding 3).
5. **[medium] The gate has no valid implementation for locked, open archives.** *Answered by R-A,
   and taken for what is gated.* Locked open archives write content, which is not held, and owe
   their receipts as today (APP.md:624). For what is held, the gate's state is two nonsecret
   facts, `retireOwed` from the file's bytes and `retireFailed`, cached with the lock screen's
   facts and refreshed after every keystore write, a failed region-writing commit included
   (`landChange` step 3), so they survive Locked and Broken and never need a handle; every held
   mutation needs a Session, and the barrier reads the handle only after `sessionLocked` has
   returned one (2.4). App tests 32 (lock after a failed retirement) and 33 (lock after a failed
   region write) are added.
6. **[medium] Page-lifetime replacement IDs suppress advice for a later compromise.** *Taken as
   written* (R-C). The suppression is scoped to a chain, the acts begun from one another's ends; an
   act begun from the page starts a new chain, and a lock ends one, so an independently begun
   compromise action gets the whole advice (3.3 (i), 3.5 `chainFor`, `chainAfter`). The
   replacement's end now offers *Change…* where the password is on and was not changed in its
   chain, so the loop revision 3 closed with page-lifetime IDs is closed inside the chain instead.
   Tested in rekey.test.ts (a replacement, a lock, then a re-keying change from its card offers
   the replacement) and state.test.ts (the chain ends at a lock).
7. **[medium] Done-state follow-ups can use the pre-change slot list.** *Taken as written* (R-C).
   The act's final state carries `After`, the committed recovery IDs, the switch and setup-needed,
   read under the state mutex once the act's commits have landed (2.4 step 8, 2.5), and `followUp`
   decides from it alone, never from the page's slots (3.5, 3.6); the store also reads the slots
   again at every such Done, with a read counter so a stale reply is never believed (3.7). App test
   36 and state.test.ts's stale-slots case (the last recovery key removed from a vault of two
   YubiKeys, `Slots` answering late) are added.
8. **[medium] The backup helper runs even when retirement did not complete.** *Taken as written*
   (R-C). `backUpAfter` is admitted only once nothing is owed; otherwise the backup is skipped with
   `BackupError` `vault.retire_owed`, the stamp unchanged, and the end says why (2.3, 3.6); a later
   `ExportBackup` meets the barrier too. App test 34.
9. **[medium] A retiring conflict can tell users to destroy committed keys.** *Taken as written*
   (R-C). One classification, `replaceResult`, serves the dialog and the Vault page's notice:
   `vault.indeterminate` and `vault.conflict`, from the replacement's commit or its retirement,
   are "unknown", which says to reopen and look at the IDs and to keep every sheet, never to
   destroy; the notice settles itself once the slots are read after the reopen (3.3, 3.5, 3.6).
   App test 37 and the rekey.test.ts and state.test.ts cases are added.
10. **[medium] The APP deltas leave the old No-match prohibition in force.** *Taken as written*
    (R-C): APP.md:1662-1664 gains the enrolment-proof exception by name and keeps the rule for
    every other case (4.1), and 3.4 points to it; the *No match* wording and the `dialogFoot` tests
    stay tied to the same condition (3.9), since revision 5 one predicate, `offersRecoverySwitch`
    (7.7, M5).
11. **[low] The password disclosure still promises no old-password prompt.** *Taken as written*
    (R-C): the change's *How this works* now says the dialog has no field for the old password
    while proving with a YubiKey still asks the current one (3.2), and APP.md §13's paragraph
    says the same (4.1); strings.test.ts pins it (3.9).

**Its gaps.** Its spawned reviews failed, and it ran nothing: the same holds here (section 6).
The GCM remnant finding is analytical: it is taken on the reasoning, which this design checked
against the codec and the cipher (2.2.3), and keystore tests 26-28's controls will show it on a
real file before showing it gone. Crash durability, layout, focus and physical erasure stay
unconfirmed, as section 6 says.

### 7.7 The critiques of revision 4

Two critics read revision 4: one for format and security correctness
(scratchpad/vmk-rotation/critique3-format.md, findings 1-12), one for the user's flows and the
specs' consistency (critique3-ux.md, M1-M9 and N1-N12). Both found it sound with changes, and both
checked what holds: the fifteen registry writes and six receipt sites of 2.4's table, the
placement table, the two passes for a legacy file, the backup's admission, the replacement's
classification, every Replace quote of section 4. Every finding is taken, the nits included; none
was found wrong or against a ruling. Where a finding left a choice to the owner, the designer made
it here, within the rulings, and says why; section 6 lists those choices for the owner to weigh
again.

The format critique:

1. **(major) A remnant past `k.size` is never scrubbed, padded, trimmed or counted.** Taken as
   written, its anchors checked (file.go:104-108, 389-408, 428-452): the handle keeps `k.end`, the
   physical end, raised before every registry write, failed or not; the trim cuts whenever the
   named end is below it; rule 2's padding, the scrub, `readRetire` and rule 7 use it; `FileSize()`
   follows it after every flip (2.2.3, rule 8; 2.6). Keystore test 28 runs on one handle with no
   reopen, in the critique's three placements and a fourth with the trim's truncation failing, and
   test 14's sync case reads the raw bytes. The option to truncate back what a failed commit
   extended is declined: the next flip's trim and scrub clear the same bytes with no second path,
   which a failed truncation there would need a seam and a clause of its own to cover (rule 8).
2. **(major) The named cure for a Tampered vault that owes keeps the owed file whole beside the
   vault.** Taken: said in 2.4 (*The import keeps a dated copy of the vault it replaces*), in
   2.2.3's and R41's limits, and in APP §2.1 and §6 (4.1, 4.4). The import's confirmation reads
   `VaultStatus.RetireOwed`, the cached fact that survives Locked, and says that the dated copy
   still opens with any unlock method the owed change removed and is best deleted once the new
   vault opens (`importCopy.replacedOwes`, 3.7). The create needs no such words: it runs only with
   no vault kept or over a damaged one (keys.go:1034-1039). The option to finish the parts that
   need no key before the copy is made is declined: the owed states that matter need the Metadata
   key, which a Locked import does not hold, so the copy would still hold them and the confirmation
   would still have to say so (2.4). With finding 3 taken, a Tampered vault owes at most a legacy
   live tail by the time it is imported over.
3. **(minor) On a Tampered vault the retirement could land without copying the region forward.**
   Taken in its first form, the choice the critique leaves to the owner, made here under R-C's
   "import or repair": `Retire` on a tampered region makes a registry-only commit in place of each
   retiring commit, writes zeros over the slot region copy neither superblock copy names, and
   scrubs, returning `ErrTampered` only while a live copy's own tail remains (2.2.3; `zeroOther`,
   2.6). Why the first form: it copies nothing forward, so R25 holds; it removes the withdrawn
   record, which after a removal without a re-key wraps the current VMK, without waiting for the
   user to import; and it makes the barrier's verdict stable, since what is left can only be
   clause 1, which no receipt touches (2.4). Its cost is one write below `0x42000` outside a
   commit, as safe as the scrub and for the same reasons (2.2.3). Keystore tests 18 and 24 and app
   test 31 are rewritten to it.
4. **(minor) The barrier reads the handle before it checks for a running ceremony.** Taken as
   written: the ceremony check is the barrier's first step, and while a ceremony holds the handle
   the barrier reads only the cached fact, refusing `ceremony.in_progress` when it says owed and
   otherwise leaving the write to `updateRegistryAtLocked`'s own refusal, as today (2.4). App test
   38 runs a create during a removal's `seams.committed` under `-race`, which part B's gate runs
   (5).
5. **(minor) A failed replacement's never-added keys open the file until the next cleanup.** Taken
   as written: `landChange` step 3 retires at once, inside the act, when a commit that failed
   before its flip left the handle owed, returning a Broken code from that retirement in place of
   the act's and otherwise the act's own (2.4). Keystore test 32 and app tests 22 and 33 fail at a
   real write through `SetCommitFault`. The same covers a failed enrolment's record.
6. **(minor) The retirement bar overstates what a copy opens when only the scrub failed.** Taken as
   written: the final state carries `RetireRemnant`, from `Keystore.RetireRemnantOnly`, and the
   bar's lead then speaks of earlier versions of the list of archives, "which the vault's old key
   reads" after a re-key and nothing more without one (2.5, 3.6, 3.7). The barrier is as ruled.
7. **(nit) `Retire` on a Tampered region is specified two ways.** Taken, through finding 3: one
   Tampered branch, stated once in 2.2.3 and in the `Retire` and `finishRetireLocked` comments;
   test 18 asserts the scrub on a file owed by both its regions and clause 4.
8. **(nit) `DeleteArchive` can remove the file and then be refused its forgetting.** Taken in its
   first form: 2.4 says that this end leaves a current record whose file is missing, read as a lost
   file's is, and that *Forget archive…* finishes it once the retirement lands. The second form,
   the delete's write passing on the first barrier's verdict, is declined: R-A names forgetting and
   deleting a record among what waits.
9. **(nit) An error from `unl.Session()` after the commit is unspecified.** Taken, with UX M4: such
   an error ends the act Done and locks the vault as an `ErrStale` reaching a service does, and
   `landChange` never returns an ordinary error after a landed commit (2.4, step 7 and the rule
   after step 8; app test 39, through the seam `failAt("session")`).
10. **(nit) R41's legacy sentence names the wrong commit.** Taken as written (4.4).
11. **(nit) The bar's list omits restoring and deleting archives.** Taken, with UX M2: one list,
    `retireHeld`, in the act's bar and the code's copy, pinned against 2.4's table (3.7, 3.9).
12. **(nit) The scrub's "no other writer" claim rests on an advisory lock.** Taken in both parts:
    2.2.3 says "no other Enfold", and the scrub reads the superblock copies again first and writes
    nothing when another writer has committed since the flip, the handle then Broken (rule 6;
    keystore test 33).

Found while revising, beside the critiques. The app's tests named `commitFault` and `writeSeen`
from revision 3 on, package-level variables of internal/keystore that a test of internal/app cannot
set; 2.6 adds exported setters, the first test hooks the keystore exports, and names the app tests
that use them. And the retirement bar's {what} for kind `entangle` was the turn-on's for turning
the password off as well, false there, since the state before asked for the password;
`retireLeadOf` leaves it out after turning the password off, as after an enrolment (3.6, 3.7).

The UX critique:

- **M1 (minor) A replacement after turning the password on is told to change the password.** Taken
  as written: the dialog's `changed` is the chain's, passed by the follow-up's press with the end's
  `after` (`from`, 3.3 (b)); `replaceFacts` decides it, and rekey.test.ts pins the turn-on case
  (3.5, 3.9); APP §13's delta says "unless a re-keying password change or turning the password on
  came earlier in the same chain" (4.1).
- **M2 (minor) The barrier's words name the wrong cause and the wrong list.** Taken as written: the
  cause "Enfold has not finished clearing the vault file's earlier state.", true wherever the owed
  state came from; the full list; "You can still open your archives and change the files inside
  them." (`retireHeld`, 3.7). The act's bar keeps a lead of its own and takes the same list (3.6);
  APP §6's Controls and notice-tier deltas follow (4.1); strings.test.ts pins the list against
  2.4's table (3.9).
- **M3 (minor) A follow-up can open a replacement the core refuses.** Taken as written: `followUp`
  answers a vault left with recovery keys alone first, on every branch, with "add" or null and
  never "change" or "replace" (3.3 (i), 3.5, 3.6); rekey.test.ts gains the recovery key's enrolment
  begun from the "add" follow-up.
- **M4 (minor) A failure after the commit landed is classified by its code alone.** Taken, with the
  format critique's nit 9: once `commit()` has returned nil the act ends Done or, for the two
  unknown outcomes, Broken (2.4); app test 39.
- **M5 (minor) The switch's condition is not a tested predicate.** Taken as written:
  `offersRecoverySwitch` in lib/lockscreen.ts, which the panel reads for both the subtle button and
  `dialogFoot`'s flag, with the cases the critique lists and the recovery key's enrolment of M6
  (3.6, 3.9). It takes the enrolment's second half as an argument, since the panel alone knows it.
- **M6 (minor) The lost-YubiKey user with no spare cannot revoke it.** Taken in its first form, the
  owner's call the critique names, made here within ruling 3, whose words name the user's
  statement that the YubiKey is lost and not the kind added: `WithRecoveryKey` serves a recovery
  key's enrolment too; `ruleYubiKey` reads "To remove this YubiKey, add a new one or a recovery key
  first: …"; the new key's showing says it can be shown again only after a YubiKey or a password
  is added (`revealCopy.digitsLineLater`); the walkthrough names the no-spare path, and app test 40
  plays it (2.3, 3.1, 3.4, 3.7). Why the first form: keeping the limit leaves a lost YubiKey, and
  with the password off its PIN, able to open the vault until a purchase; and the switch for a
  recovery key reaches less than the YubiKey's switch already does, since a recovery key so added
  is replaced with the rest by *Replace recovery keys…* and authorises no showing. DECISIONS' "Not
  done here" needs no line for it.
- **M7 (minor) The end of a failed retirement stacks three bars and invites what it says waits.**
  Taken in its first form: the skipped backup is a sentence of the retirement's bar, "The backup
  you asked for waits too.", a replacement's line is then the "not asked for" one, and the
  follow-up's line ends with "Enfold first tries again to finish clearing the vault file." (3.3,
  3.6). The follow-up is kept rather than held back: its press is the retry, and the next security
  step is worth its line. The heights are estimated again (3.6): a removal's end with a skipped
  backup is now inside the panel; a replacement's with every item still grows, once, at the act's
  last view.
- **M8 (minor) An import from a backup meets the barrier only after its whole ceremony.** Taken in
  its first form: `InspectRecords` begins without `reads`, so the barrier refuses it at its begin,
  before the proof (2.4, 2.5); MergeDialog already puts that refusal in its error notice
  (MergeDialog.svelte:110-113); app test 29 and the mock gain it.
- **M9 (minor) DESIGN's TRIM paragraph contradicts the corrected table above it.** Taken: the
  paragraph is replaced, saying that the old VMK opens nothing in the current keystore by itself,
  that an old registry recovered beside it is an earlier copy whose recovery keys and `K_P` the
  table's last rows take back, and that within the vault file R41 leaves neither (4.3).
- **N1** `One` forms for every replacement string the critique lists, chosen by `keepCount`; the
  walkthrough's quotes in the singular; strings.test.ts pins them (3.3, 3.4, 3.6, 3.7, 3.9).
- **N2** The unknown notice says "Enfold could not tell whether they replaced the old ones", true
  of a conflict and of a save not confirmed; 2.7 now gives 3.6's reading, a conflict before the
  flip being unknown too (2.7, 3.3, 3.7).
- **N3** Section 4's text blocks are reflowed at 96, as section 4 promises.
- **N4** DESIGN.md:344 becomes "from the Vault page", and 411-412 "re-wraps every way in at once
  (`FORMAT.md` §8); what the file still holds of the state before is retired next (`FORMAT.md`
  R41)" (4.3).
- **N5** strings.ts:1567-1568 and keys.go:64-65 are in 3.7's changes; keys.go:106-107 already says
  "never a field".
- **N6** "this recovery key opens the vault file by itself" (3.3, 3.7, 4.1).
- **N7** One reason, the second form: keep mode is not dismissed by Escape or the backdrop, as no
  showing of a recovery key is, so that an Escape in the readback's fields never costs the sheets
  written; the confirmation, which has no field, keeps Escape as *Cancel* (3.3, 3.7, 4.1).
- **N8** `retireFailed` and the warning are cleared on the Tampered paths (2.4); app test 31's
  second run asserts it.
- **N9** *Start again* and the switch keep the act's chain (3.4, 3.5); it is KeysPage's and has no
  component test, which 3.9 says.
- **N10** The follow-up's press hands the end's `after` to the replacement's dialog, which reads
  its count and the switch from it (`from`, `replaceFacts`; 3.3, 3.5).
- **N11** SCOPE.md:45 gains "and turning the password on" (4.2).
- **N12** APP §3's Keys delta says the act's backup is skipped while its retirement is owed, and
  APP §6's Controls delta gives `addFirstOn`'s form (4.1).

// The Vault page's acts that may re-key the vault, the part that decides
// (APP.md §13; ruled 2026-10-27): a removal, a password change, turning the
// password on, a re-key and Replace recovery keys…. Each asks the question
// whose answer re-keys, offers the backup taken after the change, and ends
// with the next step a compromise would call for. KeysPage has no component
// test, so the decisions live here, as lib/backup.ts and lib/foot.ts hold
// theirs, and the words are strings.ts's.
//
// Two rules run through it. The end's next step is read from what the act
// left committed (CeremonyState.after), never from the slots the page read
// before the act ended, which may be the old ones. And the next steps form
// one chain, the password first: within the acts begun from one another's
// ends, no end points back to recovery keys the chain made or a password
// change it made, so the chain never loops; outside it the advice is whole,
// since a later act answers a later exposure.
import { Code, CeremonyStep } from "./api";
import type { AfterFacts } from "./api";
import { enrolledAlready, nothingChanged } from "./lockscreen";
import { ceremonyCopy, codeText, dialogCopy, replaceCopy, retireLeadOf, switchCopy, vaultCopy } from "./strings";

// The committed facts an act's final state carries (CeremonyState.after),
// the core's own shape: recoveryIds may come as null for none.
export type { AfterFacts };

// The two answers of every question that may re-key (DESIGN §5).
export type Answer = "rekey" | "keep";

// The acts whose dialogs offer the backup (APP.md §13). "on" is turning the
// password on, whose ceremony kind is "entangle".
export type Act = "remove" | "password" | "on" | "rotate" | "replace";

export const WEEK = 604800;

// The ceremony kind an act runs as.
function kindOf(act: Act | "enroll" | "toToken"): string {
  return act === "on" ? "entangle" : act;
}

// ---- The question and its limit ---------------------------------------------

// Question is the fieldset a dialog that may re-key asks: its legend, the
// answer that re-keys (first and pre-selected) and the one that keeps the
// vault's key.
export interface Question {
  legend: string;
  rekey: string;
  keep: string;
}

// questionOf is the question by act and, for a removal, the type of the
// unlock method removed (DESIGN §5): a YubiKey or a recovery key is lost or
// kept; a password is known or not; turning the password on asks whether
// the file was copied, since a backup holds no YubiKey.
export function questionOf(act: "remove" | "password" | "on", type?: string): Question {
  const q = dialogCopy.rekeyQuestion;
  if (act === "on") return { legend: q.on, rekey: q.copied, keep: q.notCopied };
  if (act === "password") return { legend: q.change, rekey: q.known, keep: q.onlyMe };
  if (type === "password") return { legend: q.password, rekey: q.known, keep: q.onlyMe };
  return { legend: type === "recovery" ? q.recovery : q.hardware, rekey: q.lost, keep: q.kept };
}

// rekeyLimitOf is the info line under the question, always drawn: the
// limit of a re-key, then what the recovery keys' copies leave open and the
// act that closes it, the password first where it is on. The second
// sentence speaks of recovery keys, so it is drawn only while the vault has
// one besides any being removed (otherLeft), in the singular for exactly
// one (a count of one is singular everywhere, DECISIONS 2026-09-09); a
// YubiKey's line with the password on still says the password without them.
export function rekeyLimitOf(o: { act: "remove" | "password" | "on"; type?: string; passwordOn: boolean; otherLeft: number }): string {
  const l = dialogCopy.rekeyLimit;
  const any = o.otherLeft > 0;
  const one = o.otherLeft === 1;
  let rest = "";
  if (o.act === "on") rest = any ? (one ? l.onOne : l.on) : "";
  else if (o.act === "password") rest = any ? (one ? l.changeOne : l.change) : "";
  else if (o.type === "recovery") rest = any ? (one ? l.recoveryOne : l.recovery) + (o.passwordOn ? l.recoveryOn : "") : "";
  else if (o.type === "hardware" && o.passwordOn) rest = any ? (one ? l.methodOnOne : l.methodOn) : l.methodOnNoRecovery;
  else rest = any ? (one ? l.methodOne : l.method)(o.type === "password" ? "password" : "YubiKey") : "";
  return rest ? `${l.future} ${rest}` : l.future;
}

// ---- The backup taken after the change --------------------------------------

// backupOwed: no backup recorded here, the last over a week old, made before
// the vault was last re-keyed, or holding only replaced recovery keys
// (Vault.BackupStatus). A stamp of 0 is never; the core reads a stamp ahead
// of the clock as 0 already, and one that still reads ahead here is not
// trusted either. It asks and never refuses: nothing is gated on it.
export function backupOwed(o: { lastExportAt: number; nowMs: number; vaultRekeyed: boolean; keysReplaced: boolean }): boolean {
  if (o.lastExportAt <= 0) return true;
  const age = Math.floor(o.nowMs / 1000) - o.lastExportAt;
  if (age < 0 || age > WEEK) return true;
  return o.vaultRekeyed || o.keysReplaced;
}

// backupPossible: a backup holds the recovery keys alone (FORMAT R28), so one
// can be taken after the act only while a recovery key remains. removing is
// the type of the unlock method an act removes.
export function backupPossible(o: { recoveryKeys: number; removing?: string }): boolean {
  return o.recoveryKeys - (o.removing === "recovery" ? 1 : 0) > 0;
}

// backupTicked: whether *Export a backup afterwards* opens ticked — never,
// whatever the act and its answer (the owner, 2026-11-07: a local backup is
// a marginal feature for most users, and ticked by default it added
// confusion and copies of the key material). The tick stays, unticked, and
// the reason under the Last backup fact still says why one would help. It
// ticked on every answer that re-keyed, at a recovery key's removal and
// while a backup was owed, from 2026-10-27 until then. The arguments stay,
// so that every dialog still asks it the one question.
export function backupTicked(_o: { owed: boolean; act: Act; rekey: boolean; removing?: string }): boolean {
  return false;
}

// backupReason is the reason under the Last backup fact, by the act whose
// backup is offered (unticked by default since 2026-11-07): the replacement's (singular for one key), the removed recovery
// key's on either answer, the re-key's, the owed one's; or none.
export function backupReason(o: { owed: boolean; act: Act; rekey: boolean; removing?: string; label?: string; count?: number }): string | undefined {
  if (o.act === "replace") return o.count === 1 ? dialogCopy.backupAfter.whyReplaceOne : dialogCopy.backupAfter.whyReplace;
  if (o.act === "remove" && o.removing === "recovery") return dialogCopy.backupAfter.whyRecovery(o.label ?? "");
  if (o.rekey || o.act === "rotate") return dialogCopy.backupAfter.whyRekey;
  return o.owed ? dialogCopy.rekeyVault.backupWhy : undefined;
}

// primaryOf is the dialog's primary, naming what the press does, the backup
// included while ticked; a replacement of one key is in the singular.
export function primaryOf(act: Act, rekey: boolean, backup: boolean, count?: number): string {
  if (act === "replace" && count === 1 && !backup) return dialogCopy.replaceOne;
  return dialogCopy.primaries[act][(rekey || act === "rotate" || act === "replace" ? 1 : 0) + (backup ? 2 : 0)];
}

// lastProtector: removing this YubiKey leaves no YubiKey and no password, so
// the vault would be left with its recovery keys alone (setup needed).
export function lastProtector(slots: readonly { recipientId: string; type: string }[], removing: { recipientId: string; type: string }): boolean {
  if (removing.type !== "hardware") return false;
  return !slots.some((s) => s.recipientId !== removing.recipientId && (s.type === "hardware" || s.type === "password"));
}

// ---- The chain and the end's next step --------------------------------------

// A chain is the acts begun from one another's ends. replaced is the IDs a
// replacement in this chain made; changed, whether a re-keying password
// change or turning-on in it has ended Done.
export interface Chain {
  id: number;
  replaced: string[];
  changed: boolean;
}

// LastAct is what the page began, for the end's next step. type: the
// method a removal takes ("hardware", "recovery", "password"), or the kind
// an enrolment adds ("token", "recovery", "password"). fromFollow: an end's
// button began it. Start again and Use recovery key instead keep fromFollow
// and chain as the first begin recorded them.
export interface LastAct {
  act: Act | "enroll" | "toToken";
  type?: string;
  rekey?: boolean;
  withRecoveryKey?: boolean;
  // keepPassword: Switch to a YubiKey… with the vault's password in the same
  // words, which only their typing proves (no recovery-key switch).
  keepPassword?: boolean;
  fromFollow: boolean;
  chain: Chain | null;
}

// chainFor is the chain an act begins in: begun from an end's button, the
// one that end belongs to; begun from the page, a new chain when the act
// re-keys (the re-keying answer, a re-key, a replacement), else none.
export function chainFor(prev: Chain | null, fromFollow: boolean, act: Act | "enroll" | "toToken", rekey: boolean, nextId: number): Chain | null {
  if (fromFollow) return prev;
  return act !== "enroll" && rekey ? { id: nextId, replaced: [], changed: false } : null;
}

// chainAfter is the chain after an act's end: a replacement's new keys join
// replaced; a re-keying password change or turning-on sets changed, and so
// does a re-keying switch to a YubiKey with a new password, which chose the
// vault's password afresh in the same commit. Any other end, and any end but
// Done, leaves it as it was.
export function chainAfter(ch: Chain | null, c: { kind: string; step: string; rekey?: boolean; keepPassword?: boolean; newIds?: readonly string[] | null }): Chain | null {
  if (!ch || c.step !== CeremonyStep.StepDone) return ch;
  if (c.kind === "replace" && c.newIds?.length) return { ...ch, replaced: [...ch.replaced, ...c.newIds.filter((id) => !ch.replaced.includes(id))] };
  const chose = c.kind === "password" || c.kind === "entangle" || (c.kind === "toToken" && !c.keepPassword);
  if (chose && c.rekey && !ch.changed) return { ...ch, changed: true };
  return ch;
}

// actEnd: the step at which an act has ended with its change committed.
// Done for every act but one: a recovery key's enrolment ends showing the
// new key, at StepRecovery with no prompt, its committed facts already set
// (internal/app/keys.go BeginEnroll; ceremony.go finish keeps that step as
// the final state), and never reaches Done unless a lock landed before the
// key could be shown. The reveal's StepRecovery is not an end of this kind:
// it changes nothing.
export function actEnd(c: { kind: string; step: string; promptId?: string; after?: AfterFacts | null }): boolean {
  if (c.step === CeremonyStep.StepDone) return true;
  return c.kind === "enroll" && c.step === CeremonyStep.StepRecovery && !c.promptId && !!c.after;
}

export interface FollowUp {
  text: string;
  act: "add" | "change" | "replace";
}

// followUp is the end's next step: its sentence and the command its button
// opens, or none. Only at the end (actEnd) of the act LastAct names whose
// final state carries after, a.chain as chainAfter left it there; for a
// recovery key's enrolment that end is the recovery key's dialog, not the
// ceremony dialog's. A vault left with recovery keys alone is answered
// first on every branch: "add" after a re-keying removal of a YubiKey or an
// enrolment begun from an end's button (a recovery key added from there
// leaves the vault so still), nothing after anything else, the replacement
// being refused there and the Vault page's own line saying to add an
// unlock method. Otherwise:
//  - a re-keying removal: the password on -> "change"; else a recovery key
//    left that the chain did not make -> "replace";
//  - an enrolment begun from an end's button: the password on and the
//    chain not changed -> "change"; else as above -> "replace";
//  - a re-keying password change, or turning the password on with a
//    re-key: a recovery key left that the chain did not make -> "replace";
//  - a replacement: the password on and the chain not changed -> "change";
//  - Switch to a YubiKey… (ruled 2026-11-07): re-keyed with the same words
//    -> "change", the password first, as the house chain has it; re-keyed
//    with a new password -> "replace" for a recovery key the chain did not
//    make; not re-keyed -> "add", a second YubiKey kept apart.
// Never after a removal that kept the vault's key, a change or turning-on
// without a re-key, a re-key alone or an export; and nothing for an end with
// no after, which fails toward saying less. While the end carries a
// retirement that did not land, the sentence says the press retries it
// first. One recovery key left is said in the singular.
export function followUp(
  a: LastAct | null,
  c: { kind: string; step: string; promptId?: string; rekey?: boolean; keepPassword?: boolean; after?: AfterFacts | null; retireError?: string },
): FollowUp | null {
  const f = nextStep(a, c);
  return f && c.retireError ? { ...f, text: f.text + ceremonyCopy.followUp.retryFirst } : f;
}

function nextStep(a: LastAct | null, c: { kind: string; step: string; promptId?: string; rekey?: boolean; keepPassword?: boolean; after?: AfterFacts | null }): FollowUp | null {
  const after = c.after;
  if (!a || !after || !actEnd(c) || kindOf(a.act) !== c.kind) return null;
  const t = ceremonyCopy.followUp;
  const on = after.entangled;
  const ids = after.recoveryIds ?? [];
  const replaced = a.chain?.replaced ?? [];
  const changed = a.chain?.changed ?? false;
  // A recovery key left that this chain's replacement did not make.
  const unreplaced = ids.some((id) => !replaced.includes(id));
  const removal = c.kind === "remove" && !!c.rekey;
  const fromAdd = c.kind === "enroll" && a.fromFollow;
  // After a recovery key's removal the keys left are "the other" ones; one
  // left is singular, the end's committed count deciding.
  const other = removal && a.type === "recovery";
  const one = ids.length === 1;
  const change = other ? (one ? t.changeFirstRecoveryOne : t.changeFirstRecovery) : one ? t.changeFirstOne : t.changeFirst;
  const replace = other ? (one ? t.replaceRecoveryOne : t.replaceRecovery) : one ? t.replaceOne : t.replace;
  if (after.setupNeeded) {
    return (removal && a.type === "hardware") || fromAdd ? { act: "add", text: on ? t.addFirstOn : t.addFirst } : null;
  }
  if (removal) {
    if (on) return { act: "change", text: change };
    return unreplaced ? { act: "replace", text: replace } : null;
  }
  if (fromAdd) {
    if (on && !changed) return { act: "change", text: change };
    return unreplaced ? { act: "replace", text: replace } : null;
  }
  if ((c.kind === "password" || c.kind === "entangle") && c.rekey) return unreplaced ? { act: "replace", text: replace } : null;
  if (c.kind === "replace") return on && !changed ? { act: "change", text: t.changeAfterReplace } : null;
  if (c.kind === "toToken") {
    if (!c.rekey) return { act: "add", text: switchCopy.addSecond };
    if (c.keepPassword) return { act: "change", text: change };
    return unreplaced ? { act: "replace", text: replace } : null;
  }
  return null;
}

// ---- Replace recovery keys… ---------------------------------------------------

// replaceGreyed is why Replace recovery keys… is greyed, or nothing: locked,
// Tampered, no recovery key to replace, or every unlock method a recovery
// key, where a new key could be shown again only through a YubiKey or a
// password the vault does not have.
export function replaceGreyed(f: { unlocked: boolean; tampered: boolean; recoveryKeys: number; setupNeeded: boolean }): string | undefined {
  if (!f.unlocked) return codeText(Code.CodeNeedsUnlock);
  if (f.tampered) return vaultCopy.changesOff;
  if (f.recoveryKeys <= 0) return vaultCopy.noRecoveryToReplace;
  if (f.setupNeeded) return vaultCopy.replaceNeedsProtector;
  return undefined;
}

// replaceFacts is what the replacement's dialog reads: opened from an end's
// button (from), its count and the switch from that end's committed facts
// and changed from the chain, since the page's slots and switch may still be
// the reads from before that act; opened from the page, from the store, and
// changed false, a new chain beginning with it. changed leaves out the
// sentences that say to change the password too.
export function replaceFacts(o: { from?: { after: AfterFacts; changed: boolean }; recoveryKeys: number; entangled: boolean }): { count: number; passwordOn: boolean; changed: boolean } {
  if (o.from) return { count: (o.from.after.recoveryIds ?? []).length, passwordOn: o.from.after.entangled, changed: o.from.changed };
  return { count: o.recoveryKeys, passwordOn: o.entangled, changed: false };
}

// The idle lock's default, in minutes (settings.go defaultIdle).
const IDLE_DEFAULT = 10;

// replaceMinutes is the idle time the replacement's ready line names: the
// status's idleEffectiveMinutes, the minutes the core runs the session on
// with its clamp applied (settings.go timeouts), never the setting as
// stored. The settings answer the registry's minutes as they are, and the
// core runs a value out of range on the default instead: an imported vault
// that stores 60 locks after 10, and a line that said 60 would promise time
// the session does not give. The default stands in only while the status
// names no number: 0 while not unlocked, or no status read yet.
export function replaceMinutes(st: { idleEffectiveMinutes: number } | null | undefined): number {
  return st && st.idleEffectiveMinutes > 0 ? st.idleEffectiveMinutes : IDLE_DEFAULT;
}

// What a replacement's end means for the keys it showed, one answer for the
// dialog and for the Vault page's notice: "replaced" at Done; "unknown" when
// it failed with vault.indeterminate or vault.conflict after keys were
// shown, from its commit or its retirement, so the keys may be the vault's
// and nothing says to destroy them; "notAdded" when it failed any other way
// after keys were shown, which can only be before its commit landed (a
// landed commit ends Done, or Broken with one of those two codes); nothing
// otherwise.
export type ReplaceResult = "replaced" | "notAdded" | "unknown";

export function replaceResult(c: { kind: string; step: string; error?: string; newIds?: readonly string[] | null }): ReplaceResult | null {
  if (c.kind !== "replace") return null;
  if (c.step === CeremonyStep.StepDone) return "replaced";
  if (c.step !== CeremonyStep.StepFailed || !c.newIds?.length) return null;
  return c.error === Code.CodeIndeterminate || c.error === Code.CodeConflict ? "unknown" : "notAdded";
}

// The Vault page's notice about keys a replacement showed and did not
// replace with: their IDs, and whether it is known that they were never
// added. An ID is not secret (it is plaintext in the vault file), so the
// store keeps the notice across a lock and a Broken state.
export interface ReplaceNotice {
  ids: string[];
  known: boolean;
}

// replaceNotice is the notice after an event: a "notAdded" end raises it as
// known, an "unknown" one as not known; a Done, and any event of a
// replacement still running (a new one has begun), clears it; an unrelated
// event leaves it. listed, the recovery keys' IDs read after the reopen and
// an unlock, settles one not known: every ID listed, the replacement landed
// and the cards say so, so it goes; none listed, it is known never added.
export function replaceNotice(
  prev: ReplaceNotice | null,
  c: { kind: string; step: string; error?: string; newIds?: readonly string[] | null } | null,
  listed?: readonly string[],
): ReplaceNotice | null {
  let next = prev;
  if (c && c.kind === "replace") {
    const r = replaceResult(c);
    if (r === "notAdded" || r === "unknown") next = { ids: [...(c.newIds ?? [])], known: r === "notAdded" };
    else if (r === "replaced" || c.step !== CeremonyStep.StepFailed) next = null;
  }
  if (next && !next.known && listed) {
    if (next.ids.every((id) => listed.includes(id))) return null;
    if (next.ids.every((id) => !listed.includes(id))) return { ids: next.ids, known: true };
  }
  return next;
}

// ReceiptRow is one row of the confirmation and of the end's receipt: the
// key's name, then which new key replaces which old one.
export interface ReceiptRow {
  label: string;
  text: string;
}

// pairRows pairs each new key with the one it replaces (the new key at
// keepIndex replaces oldIds[keepIndex-1]) under the name the old key had,
// which the new one takes. The name is read from the cards by either ID,
// since the cards are read again once the act ends.
export function pairRows(c: { oldIds?: readonly string[] | null; newIds?: readonly string[] | null }, slots: readonly { recoveryId?: string; label: string }[]): ReceiptRow[] {
  const olds = c.oldIds ?? [];
  return (c.newIds ?? []).map((id, i) => {
    const old = olds[i] ?? "";
    const label = slots.find((s) => !!s.recoveryId && (s.recoveryId === old || s.recoveryId === id))?.label ?? ceremonyCopy.recoveryKey;
    return { label, text: replaceCopy.pair(id, old) };
  });
}

// An item of a dialog's end under its title: an attention bar or a plain
// line in the body ink.
export interface EndItem {
  tone: "attention" | "plain";
  text: string;
}

// replaceBackupItems is a replacement's end by what its backup did: written,
// the line that says when to destroy the replaced sheets; not asked for, a
// bar that no backup holds the new keys; failed, the shared bar with why and
// then when to destroy them; skipped because the retirement did not land,
// no bar of its own (the retirement's bar says the backup waits) and the
// not-asked-for bar. One key is singular.
export function replaceBackupItems(c: { kind: string; backedUp?: boolean; backupError?: string; keepCount?: number; newIds?: readonly string[] | null }): EndItem[] {
  if (c.kind !== "replace") return [];
  const one = (c.keepCount || c.newIds?.length || 0) === 1;
  const r = replaceCopy;
  if (c.backedUp) return [{ tone: "plain", text: one ? r.backedUpOne : r.backedUp }];
  if (c.backupError && c.backupError !== Code.CodeRetireOwed) {
    return [
      { tone: "attention", text: ceremonyCopy.backupNotSaved(codeText(c.backupError)) },
      { tone: "plain", text: one ? r.afterFailedBackupOne : r.afterFailedBackup },
    ];
  }
  return [{ tone: "attention", text: one ? r.noBackupOne : r.noBackup }];
}

// failedNotes are the captions under a failed act's danger bar: that
// nothing was changed, for the acts whose failure before the change landed
// leaves the file as it was (lockscreen.ts nothingChanged; never for a save
// that could not be confirmed or a conflict, which Broken words); and, for a
// replacement that showed keys, what is known of them (replaceResult), in
// the singular for one key shown.
export function failedNotes(c: { kind: string; step: string; error?: string; newIds?: readonly string[] | null; generated?: boolean }): string[] {
  if (c.step !== CeremonyStep.StepFailed) return [];
  // Switch to a YubiKey… says what still opens the vault, and, after a key
  // was made on the YubiKey, that it stays there (ruled 2026-11-07).
  if (c.kind === "toToken") return [...(nothingChanged(c) ? [switchCopy.nothingChanged] : []), ...(c.generated ? [switchCopy.keyLeft] : [])];
  const out = nothingChanged(c) ? [ceremonyCopy.nothingChanged] : [];
  const r = replaceResult(c);
  const one = c.newIds?.length === 1;
  if (r === "notAdded") out.push(one ? ceremonyCopy.keysNeverAddedOne : ceremonyCopy.keysNeverAdded);
  if (r === "unknown") out.push(one ? ceremonyCopy.keysWorkIfListedOne : ceremonyCopy.keysWorkIfListed);
  return out;
}

// retireBar is the attention bar of an end whose retirement did not land
// (CeremonyState.retireError): what the file still holds, why, what waits,
// and the backup asked for when it waits too.
export function retireBar(c: { kind: string; rekey?: boolean; keepCount?: number; retireError?: string; retireRemnant?: boolean; backupError?: string; after?: { entangled: boolean } | null }): string | undefined {
  if (!c.retireError) return undefined;
  return ceremonyCopy.retireNotFinished(retireLeadOf(c), codeText(c.retireError), c.backupError === Code.CodeRetireOwed);
}

// backupBar is the attention bar of an end whose backup was not saved, the
// change standing; none for vault.retire_owed, which the retirement's bar
// says. A replacement's end says it among its own items instead.
export function backupBar(c: { kind: string; backupError?: string }): string | undefined {
  if (!c.backupError || c.backupError === Code.CodeRetireOwed || c.kind === "replace") return undefined;
  return ceremonyCopy.backupNotSaved(codeText(c.backupError));
}

// afterRecoveryProof: the end of an enrolment of a YubiKey begun with Use
// recovery key instead says the lost YubiKey is the next to remove; not any
// enrolment proved with a recovery key, since a vault of recovery keys alone
// proves every enrolment so, and not a recovery key's, which ends in the
// recovery key's dialog.
export function afterRecoveryProof(c: { kind: string; step: string }, a: LastAct | null): boolean {
  return c.kind === "enroll" && c.step === CeremonyStep.StepDone && a?.act === "enroll" && a.type === "token" && !!a.withRecoveryKey;
}

// digitsLater: the recovery key just added was proved with Use recovery key
// instead, so no YubiKey or password is at hand to show it again with, and
// its digits' caption says when it can be (revealCopy.digitsLineLater).
export function digitsLater(a: LastAct | null): boolean {
  return a?.act === "enroll" && a.type === "recovery" && !!a.withRecoveryKey;
}

// backupSavedOnClose: closing the dialog on this end shows the backup's done
// line in the status bar, as a plain export's does: an export's Done, or an
// act's Done that wrote the backup it was asked for (CeremonyState.backedUp).
export function backupSavedOnClose(c: { kind: string; step: string; backedUp?: boolean } | null | undefined): boolean {
  return !!c && c.step === CeremonyStep.StepDone && (c.kind === "export" || !!c.backedUp);
}

// ceremonyTitleOf is the ceremony dialog's title, by kind: the act it names,
// a replacement's in the singular for one key (its keepCount, or before the
// first key is shown, the cards' count).
export function ceremonyTitleOf(c: { kind: string; keepCount?: number }, recoveryKeys: number): string {
  if (c.kind === "replace") return (c.keepCount || recoveryKeys) === 1 ? vaultCopy.ceremonyTitle.replaceOne : vaultCopy.ceremonyTitle.replace;
  return vaultCopy.ceremonyTitle[c.kind] ?? vaultCopy.ceremonyTitle.export;
}

// removeRule is a card's greyed Remove… where the two-method rule stops it:
// a YubiKey's says what to add first, and the vault's last YubiKey's that a
// password can follow it once it is gone (ruled 2026-11-07, option A); a
// password's names Switch to a YubiKey… on its card (ruled 2026-11-07);
// a recovery key's the rule itself. last: no other YubiKey is left.
export function removeRule(type: string, last = false): string {
  if (type === "hardware") return last ? vaultCopy.ruleLastYubiKey : vaultCopy.ruleYubiKey;
  return type === "password" ? vaultCopy.rulePassword : vaultCopy.rule;
}

// ---- Switch to a YubiKey… (ruled 2026-11-07) ------------------------------

// switchShown: the command stands on a password's card, on a vault with no
// YubiKey; on any vault without a standalone password it is not drawn.
export function switchShown(card: { type: string }, hasHardware: boolean): boolean {
  return card.type === "password" && !hasHardware;
}

// switchGreyed is why Switch to a YubiKey… is greyed, or nothing, in the
// order the page ranks the reasons (judge E1): locked, Tampered (the house's
// two), then no recovery key, first among the vault's own, since the switch
// would leave a lost YubiKey with no way back; then a second standalone
// password, since a YubiKey carries one password with it. The core's own
// reason (EntangledState.switchReason) is read beside the cards' count, so
// that a refusal the cards cannot show is still said, in its own words.
export function switchGreyed(f: { unlocked: boolean; tampered: boolean; recoveryKeys: number; passwords: number; reason?: string }): string | undefined {
  if (!f.unlocked) return codeText(Code.CodeNeedsUnlock);
  if (f.tampered || f.reason === Code.CodeVaultTampered) return vaultCopy.changesOff;
  if (f.recoveryKeys <= 0 || f.reason === Code.CodeNoRecoverySlot) return switchCopy.noRecovery;
  if (f.passwords > 1 || f.reason === Code.CodeSlotPolicy) return switchCopy.twoPasswords;
  return f.reason ? codeText(f.reason) : undefined;
}

// switchPrimary is the form's primary, naming its press: the switch, and
// the re-key with it on the first answer. There is no backup form (ruled
// 2026-11-07): the act takes none.
export function switchPrimary(rekey: boolean): string {
  return switchCopy.primary[rekey ? 1 : 0];
}

// switchEndItems are the lines of Switch to a YubiKey…'s Done under its
// title, before the retirement's bar and the next step: what now opens the
// vault, by the password chosen, and what a loss of this YubiKey means.
// label is the YubiKey's (SlotLabel); n the vault's recovery keys, from the
// end's committed facts.
export function switchEndItems(c: { kind: string; step: string; keepPassword?: boolean; slotLabel?: string; after?: AfterFacts | null }, recoveryKeys: number): EndItem[] {
  if (c.kind !== "toToken" || c.step !== CeremonyStep.StepDone) return [];
  const label = c.slotLabel || "The YubiKey";
  const n = c.after ? (c.after.recoveryIds ?? []).length : recoveryKeys;
  return [
    { tone: "plain", text: c.keepPassword ? switchCopy.doneSame(label) : switchCopy.doneNew(label) },
    { tone: "plain", text: switchCopy.ifLost(label, n) },
  ];
}

// ---- The password's row and option A (ruled 2026-11-07) ----------------------

// passwordRow is the Password with every YubiKey row by the vault's ways in
// (judge E4): its line, and why Turn on… or Change… is greyed where the
// switch can take effect only through Switch to a YubiKey…. Beside a
// standalone password, off: nothing to turn on until a YubiKey comes;
// on (a vault left so before option A): the switch's words are set by the
// switch, so Change… is greyed and Turn off… stays. On recovery keys alone
// and on: the next YubiKey added inherits it, Change… and Turn off… live.
// Otherwise the row's two lines as before.
export function passwordRow(f: { on: boolean; hasHardware: boolean; hasStandalone: boolean }): { line: string; turnOnWhy?: string; changeWhy?: string } {
  if (!f.hasHardware && f.hasStandalone) {
    return f.on ? { line: vaultCopy.passwordOnNoYubiKey, changeWhy: vaultCopy.changeBySwitch } : { line: vaultCopy.passwordOffNoYubiKey, turnOnWhy: vaultCopy.turnOnNeedsYubiKey };
  }
  if (!f.hasHardware && f.on) return { line: vaultCopy.passwordOnNext };
  return { line: f.on ? vaultCopy.passwordOn : vaultCopy.passwordOff };
}

// passwordAlone: Add an unlock method… with Password chosen on a vault whose
// unlock methods are recovery keys alone, where the password will open the
// vault by itself (option A): the attention bar is drawn, and with the
// switch on the line that it ends here, which the core does in the same
// commit (keystore AddSlot).
export function passwordAlone(kind: string, slots: readonly { type: string }[]): boolean {
  return kind === "password" && slots.length > 0 && slots.every((s) => s.type === "recovery");
}

// ---- The proof ---------------------------------------------------------------

// recoveryNoteOf is the recovery view's note in a dialog, by why a recovery
// key proves it: a YubiKey the removal takes away is not asked for; the user
// chose the recovery key instead of a YubiKey (Use recovery key instead, on
// the wait, the PIN's form, a blocked PIN or the password asked alone), by
// the kind the enrolment adds; the kinds that always ask a recovery key (a
// backup's test, an import, the merge's inspection), theirs; any other act
// proves with one because every unlock method is a recovery key, with what
// comes next for an enrolment, in the singular where the vault has one
// recovery key (recoveryKeys: a vault never set up, whose Vault page adds
// its first unlock method). Never by kind alone: adding a password to a
// vault of recovery keys would read "instead of a YubiKey".
export function recoveryNoteOf(c: { kind: string; without?: string }, a: LastAct | null, recoveryKeys: number): string {
  const n = ceremonyCopy.recoveryNote;
  if (c.without) return n.remove;
  // Switch to a YubiKey… with a new password, proved by a recovery key
  // (ruled 2026-11-07): the recovery key stands in for the password.
  if (c.kind === "toToken") return switchCopy.recoveryNote;
  if (c.kind === "enroll" && a?.act === "enroll" && a.withRecoveryKey) return a.type === "recovery" ? n.enrollRecovery : n.enroll;
  if (c.kind === "verify" || c.kind === "import" || c.kind === "records") return n[c.kind];
  return (recoveryKeys === 1 ? n.onlyOne : n.only) + (c.kind === "enroll" ? n.nextMethod : "");
}

// swapWordsOf is what an enrolment's swap view says (APP.md §6): the
// proving YubiKey to come out first, so the same one is not added twice
// (keys.go enrollToken), in the words it has had since 2026-10-17; or a
// YubiKey that already unlocks this vault, inserted as the new one and
// refused on insertion with nothing asked of it (ruled 2026-11-06, R1 and
// R5), told from the first by the core's note (lib/lockscreen
// enrolledAlready). Both name the YubiKey by the unlock method's label
// (removeLabel), never by slotLabel, which may still hold the proof's.
export function swapWordsOf(c: { step: CeremonyStep; removeLabel?: string; error?: string }): { lead: string; line: string; already: boolean } {
  const label = c.removeLabel ?? "";
  if (enrolledAlready(c)) return { lead: ceremonyCopy.alreadyLead(label), line: ceremonyCopy.alreadyLine, already: true };
  return { lead: ceremonyCopy.swapLead(label), line: ceremonyCopy.swapLine, already: false };
}

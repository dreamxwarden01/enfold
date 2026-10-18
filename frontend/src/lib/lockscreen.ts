// The lock screen's stage (APP.md §6, ruled 2026-10-17): one panel that
// changes in place, a step line above it while a ceremony runs, and the
// touch's green over the whole stage. Pure — given the status's facts and
// the ceremony's event, what the stage shows — so that the decisions can
// be tested apart from the component. The steps themselves are the
// core's (APP.md §2.2); this only says where each one is drawn.
import { CeremonyStep } from "./api";

// Way is the ceremony's way in, as far as the page knows it.
export type Way = "token" | "password" | "recovery" | "";

// wayOf: the core's own Method once it has named it; before that, an
// unlock's is the button the page pressed, and the step is the last
// evidence (a PIN or a touch is the YubiKey's, a password or a recovery
// key prompt is not).
export function wayOf(method: string, begun: Way, c: { kind: string; step: CeremonyStep } | null | undefined): Way {
  if (method === "token" || method === "password" || method === "recovery") return method;
  if (!c) return begun;
  if (c.kind === "unlock" && begun) return begun;
  switch (c.step) {
    case CeremonyStep.StepPIN:
    case CeremonyStep.StepTouch:
    case CeremonyStep.StepProbing:
    case CeremonyStep.StepTwoKeys:
    case CeremonyStep.StepNoMatch:
      return "token";
    case CeremonyStep.StepRecovery:
      return "recovery";
    case CeremonyStep.StepPassword:
      return "password";
  }
  return "";
}

// View is what the panel holds.
//   firstrun  no vault yet, or the configured one absent or damaged
//   rest      locked, nothing running
//   insert    the key art: waiting for the YubiKey, two in, reading it
//   edge      the YubiKey is not this vault's, or another program holds it
//   secret    a PIN, a password, a management key (SecretInput)
//   recovery  the recovery key's cells (RecoveryInput)
//   touch     the green: the touch, then the derivation, the release and
//             Done of a YubiKey's way in — one view, so the rings stay
//   opening   the derivation and the release of a typed way in, neutral
//   done      Done of a typed way in, neutral
//   failed    the ceremony stopped, said on the step where it stopped
//   blocked   the PIN is blocked (the core parks there until a cancel)
//   swap      an enrolment's swap in a dialog: the YubiKey that proved it
//             is you to come out before the new one goes in (dialogViewOf)
//   wait      any other step that asks nothing here (records, a swap, a
//             recovery key being shown)
//   hold      nothing new to draw: keep what is on the panel
export type View = "firstrun" | "rest" | "insert" | "edge" | "secret" | "recovery" | "touch" | "opening" | "done" | "failed" | "blocked" | "swap" | "wait" | "hold";

export interface StageFacts {
  // running: a ceremony is on the screen, the settle after the unlock
  // included.
  running: boolean;
  firstRun: boolean;
  way: Way;
  c: { kind: string; step: CeremonyStep; error?: string; promptId?: string; removeLabel?: string } | null | undefined;
  // passed: the ceremony on the screen has been at a step other than its
  // opening WaitingForKey (passedAfter), so a WaitingForKey now is a real
  // wait for a YubiKey — a setup's or an import's enrolment after the
  // recovery key, a create's after the chosen password.
  passed?: boolean;
  // afterTouch: the touch is behind this event with nothing asked since —
  // the derivation, the release and Done that follow it (passedAfter).
  afterTouch?: boolean;
}

// failedShown: a ceremony that stopped for a reason the user did not give
// stays on the panel until it is dismissed or begun again — while the
// core still parks on it, and for an unlock after the state went back to
// Locked too (the other kinds say their failure on the rest or first-run
// panel, as a bar).
export function failedShown(f: StageFacts): boolean {
  const c = f.c;
  return !!c && c.step === CeremonyStep.StepFailed && c.error !== "ceremony.cancelled" && (f.running || c.kind === "unlock");
}

// viewOf is the view the facts ask for.
export function viewOf(f: StageFacts): View {
  if (failedShown(f)) return "failed";
  if (!f.running) return f.firstRun ? "firstrun" : "rest";
  const c = f.c;
  if (!c) return "hold";
  switch (c.step) {
    case CeremonyStep.StepWaitingForKey:
      // A ceremony opens at WaitingForKey whatever its way (APP.md §2.2):
      // a password's or a recovery key's moves on at once, and drawing
      // the key art for that instant would flash it. Every other
      // WaitingForKey is a wait for a YubiKey and is drawn as one: the
      // YubiKey's way in, and any wait after another step of the same
      // ceremony — a create's, a setup's or an import's enrolment, a
      // YubiKey pulled mid-ceremony — whatever method the core named.
      return f.way === "token" || f.passed ? "insert" : "hold";
    case CeremonyStep.StepSwapKey:
      // An enrolment's token flow opens at SwapKey (APP.md §3). With
      // nothing to remove — a create, a setup, a backup's import: no
      // YubiKey unlocked anything — it is the wait for the YubiKey to
      // add, the key art's; a swap proper asks nothing here.
      return c.removeLabel ? "wait" : "insert";
    case CeremonyStep.StepTwoKeys:
    case CeremonyStep.StepProbing:
      return "insert";
    case CeremonyStep.StepNoMatch:
    case CeremonyStep.StepBusy:
      return "edge";
    case CeremonyStep.StepPIN:
    case CeremonyStep.StepPassword:
    case CeremonyStep.StepManagementKey:
      return "secret";
    case CeremonyStep.StepRecovery:
      return c.promptId ? "recovery" : "wait";
    case CeremonyStep.StepTouch:
      return "touch";
    // The green holds through whatever follows a touch in the same
    // ceremony (APP.md §6, ruled 2026-10-17): a create's or a setup's
    // derivation after its YubiKey's touch as much as an unlock's, whatever
    // way the core named. An unlock by the YubiKey keeps it too when the
    // page saw no touch (one joined late, a touch the YubiKey cached).
    case CeremonyStep.StepDeriving:
    case CeremonyStep.StepReleasing:
      return f.way === "token" || f.afterTouch ? "touch" : "opening";
    case CeremonyStep.StepDone:
      return f.way === "token" || f.afterTouch ? "touch" : "done";
    case CeremonyStep.StepBlocked:
      return "blocked";
    case CeremonyStep.StepFailed:
      return "hold"; // cancelled: the next event says what follows
  }
  return "wait";
}

// Passed is what passedAfter keeps of the ceremony on the screen: its
// kind; whether it has left its opening WaitingForKey (passed); whether a
// touch is behind it with nothing asked since (afterTouch), so that the
// green holds through the derivation, the release and Done that follow
// it; whether its last event was a derivation (deriving); and whether a
// derivation is behind it (opened) — a setup's or an import's first one
// opens the vault or the backup, the one after it adds the first way in
// (derivingOf).
export interface Passed {
  kind: string;
  passed: boolean;
  afterTouch: boolean;
  deriving: boolean;
  opened: boolean;
}

export const NOT_PASSED: Passed = { kind: "", passed: false, afterTouch: false, deriving: false, opened: false };

// passedAfter follows the ceremony's events. Done and Failed end it, so
// the WaitingForKey after them opens the next one; so does a new kind.
// Done keeps afterTouch: it is drawn on the touch's green.
export function passedAfter(prev: Passed, c: { kind: string; step: CeremonyStep } | null | undefined): Passed {
  // Compared by value: the page may hold a copy (a $state proxy).
  if (!c) return samePassed(prev, NOT_PASSED) ? prev : NOT_PASSED;
  const same = c.kind === prev.kind;
  const opening = c.step === CeremonyStep.StepWaitingForKey;
  const ended = c.step === CeremonyStep.StepDone || c.step === CeremonyStep.StepFailed;
  const passed = ended ? false : !same ? !opening : prev.passed || !opening;
  const follows = c.step === CeremonyStep.StepDeriving || c.step === CeremonyStep.StepReleasing || c.step === CeremonyStep.StepDone;
  const afterTouch = c.step === CeremonyStep.StepTouch || (follows && same && prev.afterTouch);
  const deriving = c.step === CeremonyStep.StepDeriving;
  // A derivation said again (a note on it) is still the same one.
  const opened = !ended && same && (prev.opened || (prev.deriving && !deriving));
  const next: Passed = { kind: c.kind, passed, afterTouch, deriving, opened };
  return samePassed(prev, next) ? prev : next;
}

function samePassed(a: Passed, b: Passed): boolean {
  return a.kind === b.kind && a.passed === b.passed && a.afterTouch === b.afterTouch && a.deriving === b.deriving && a.opened === b.opened;
}

// Derivation is what a derivation says it does, by the act (APP.md §6,
// ruled 2026-10-17): an unlock or a test opens the vault; a create
// creates it — there is no vault to open yet; a setup or an import, past
// the derivation that opened the vault or the backup, adds its first way
// in. ("Opening the vault…" was said for all of them, a create's
// included.)
export type Derivation = "opening" | "creating" | "adding";

export function derivingOf(kind: string, opened: boolean): Derivation {
  if (kind === "create") return "creating";
  if ((kind === "setup" || kind === "import") && opened) return "adding";
  return "opening";
}

// shownAfter is the view on the panel after the facts ask for `want`:
// "hold" keeps what is there; with nothing there yet, the quiet one.
export function shownAfter(prev: View | null, want: View): View {
  if (want !== "hold") return want;
  return prev ?? "wait";
}

// FOUND_HOLD: the key found, its LED held solid this long before the
// panel moves on (APP.md §6, Motion: Unlock).
export const FOUND_HOLD = 200;

// holdBefore is how long the panel keeps `from` before it turns to `to`:
// the key art holds its found LED when the YubiKey answered, and nothing
// else waits. A failure or a cancel never waits.
export function holdBefore(from: View | null, to: View): number {
  if (from !== "insert" || to === "insert") return 0;
  return to === "failed" || to === "rest" || to === "firstrun" ? 0 : FOUND_HOLD;
}

// Led is the key art's LED (KeyArt.svelte): breathing while the app
// listens, solid once the YubiKey answered, dim and still when nothing can
// be heard from the reader, two YubiKeys when two are in.
export type Led = "listen" | "found" | "still" | "off" | "two";

// insertLed: the insert view's LED, from the step the view holds (kept
// under the found LED while the core moves on) and the event. The Smart
// Card service not running (token.no_service, said after ten seconds at
// the insert step) stops the breathing: the app is not hearing anything
// (APP.md §6, ruled 2026-10-17).
export function insertLed(held: CeremonyStep, found: boolean, c: { step: CeremonyStep; error?: string } | null | undefined): Led {
  if (held === CeremonyStep.StepTwoKeys) return "two";
  if (held !== CeremonyStep.StepWaitingForKey || found) return "found";
  return c?.step === CeremonyStep.StepWaitingForKey && c.error === "token.no_service" ? "still" : "listen";
}

// padlockOpen: the header's glyph says what its word says (APP.md §6,
// Motion: Lock and Done). Open on arrival until the lock settles, closed
// at rest, and open again at an unlock's Done, where the word is
// *Unlocked* — a create's, a setup's or a test's Done opens nothing.
export function padlockOpen(settled: boolean, running: boolean, c: { kind: string; step: CeremonyStep } | null | undefined): boolean {
  return !settled || (running && c?.kind === "unlock" && c.step === CeremonyStep.StepDone);
}

// pinLeads: whether the secret view of a ceremony heads with the PIN's
// own instruction. A ceremony with a step line (an unlock) names the step
// there; one without heads its PIN's step as its YubiKey's and touch's
// steps are headed, never with the act's name the header already says
// (APP.md §6, ruled 2026-10-17).
export function pinLeads(kind: string | undefined, shows: string): boolean {
  return kind !== "unlock" && (shows === "pin" || shows === "both");
}

// green: the whole stage wears the touch's gradient (APP.md §6, ruled
// 2026-09-06: the touch moment is a full-surface takeover).
export function green(v: View): boolean {
  return v === "touch";
}

// Phase is how far along the step line a ceremony is: the index of the
// live step, or Past once every step is done.
export const PAST = 99;

// LineKind: which step line a ceremony draws. Only an unlock has fixed
// steps; the setup, the import and the backup test have none, and a
// password's way in is one step, which a line would not help.
export type LineKind = "token" | "recovery" | "";

export function lineKind(kind: string | undefined, way: Way): LineKind {
  if (kind !== "unlock") return "";
  return way === "token" ? "token" : way === "recovery" ? "recovery" : "";
}

// phaseOf is the live step's index for step, given the phase before it: a
// failure, a park and a cancel stay where the ceremony was — a touch that
// timed out is the touch's, wherever the page joined.
export function phaseOf(line: LineKind, step: CeremonyStep, prev: number, error = ""): number {
  if (line === "token") {
    switch (step) {
      case CeremonyStep.StepWaitingForKey:
      case CeremonyStep.StepTwoKeys:
      case CeremonyStep.StepProbing:
      case CeremonyStep.StepNoMatch:
      case CeremonyStep.StepBusy:
        return 0;
      case CeremonyStep.StepPIN:
      case CeremonyStep.StepPassword:
      case CeremonyStep.StepManagementKey:
      case CeremonyStep.StepBlocked:
        return 1;
      case CeremonyStep.StepTouch:
        return 2;
      case CeremonyStep.StepDeriving:
      case CeremonyStep.StepReleasing:
      case CeremonyStep.StepDone:
        return PAST;
      case CeremonyStep.StepFailed:
        return error === "token.touch" ? 2 : prev === PAST ? 2 : Math.max(prev, 0);
    }
    return Math.max(prev, 0);
  }
  if (line === "recovery") {
    switch (step) {
      case CeremonyStep.StepRecovery:
      case CeremonyStep.StepWaitingForKey:
        return 0;
      case CeremonyStep.StepDeriving:
      case CeremonyStep.StepReleasing:
        return 1;
      case CeremonyStep.StepDone:
        return PAST;
      case CeremonyStep.StepFailed:
        return prev === PAST ? 1 : Math.max(prev, 0);
    }
    return Math.max(prev, 0);
  }
  return 0;
}

// Phased is the phase with the event it was last advanced by, so that a
// failure, which carries no step of its own, is drawn where the last
// event left the ceremony. touched: the vault's password is asked again
// after the touch (a wrong one is known only then, APP.md §2.2), so the
// PIN's step is live once more while the touch is already done.
export interface Phased {
  seq: number;
  phase: number;
  touched: boolean;
}

export const NO_PHASE: Phased = { seq: -1, phase: 0, touched: false };

export function phaseAfter(prev: Phased, line: LineKind, c: { seq: number; step: CeremonyStep; error?: string; n?: number } | null | undefined): Phased {
  if (!c) return prev.seq === -1 ? prev : NO_PHASE;
  if (c.seq === prev.seq) return prev;
  const phase = phaseOf(line, c.step, prev.phase, c.error ?? "");
  return { seq: c.seq, phase, touched: touchedAfter(prev, line, c) };
}

// touchedAfter: the touch is behind a password prompt when the ceremony
// was past the touch before it, or — for a page that joined there — when
// the event counts a touch already made on this YubiKey (n). A failure
// keeps what was; any other step clears it.
function touchedAfter(prev: Phased, line: LineKind, c: { step: CeremonyStep; n?: number }): boolean {
  if (line !== "token") return false;
  if (c.step === CeremonyStep.StepPassword) return prev.touched || prev.phase === PAST || prev.phase === 2 || (c.n ?? 0) > 0;
  if (c.step === CeremonyStep.StepFailed) return prev.touched;
  return false;
}

export type Disc = "done" | "live" | "next";

// discs is each step's state on the line: the ones before the live step
// done, the live one live, the rest to come — except the touch, done
// while the password is asked again after it (touched).
export function discs(count: number, phase: number, touched = false): Disc[] {
  return Array.from({ length: count }, (_, i) => (i < phase || (touched && i === 2 && phase < i) ? "done" : i === phase ? "live" : "next"));
}

// explicitMethod is the way in an explicit Unlock begins with — the
// locked banner's, the foot's, the Vault page's, an Explorer open's — so
// that the lock scene opens on the ceremony already running, as the
// tray's Unlock… does (APP.md §6, ruled 2026-10-17): the YubiKey where
// the vault has one, else its password. Only a vault at rest: one being
// unlocked is already shown, and one that needs setting up, is open in
// another Enfold, is broken or is not there has its own panel to read
// first. An idle lock or a Windows lock never comes this way.
export function explicitMethod(s: { state: string; setupNeeded: boolean; hasHardwareSlot: boolean; hasPasswordSlot: boolean } | null | undefined): "token" | "password" | null {
  if (!s || s.state !== "locked" || s.setupNeeded) return null;
  if (s.hasHardwareSlot) return "token";
  return s.hasPasswordSlot ? "password" : null;
}

// Halves is how far an enrolment in a dialog is along its two-part line
// (APP.md §13, ruled 2026-10-17): proving it is you with a way in the
// vault already has, then adding the new one. The core's SwapKey, or a
// prompt that chooses a new secret, is the turn; Done passes both.
export interface Halves {
  seq: number;
  phase: number;
}

export const NO_HALVES: Halves = { seq: -1, phase: 0 };

export function halvesAfter(prev: Halves, c: { seq: number; kind: string; step: CeremonyStep; choose?: boolean } | null | undefined): Halves {
  if (!c || c.kind !== "enroll") return prev.seq === -1 ? prev : NO_HALVES;
  if (c.seq === prev.seq) return prev;
  if (c.step === CeremonyStep.StepDone) return { seq: c.seq, phase: PAST };
  const turned = c.step === CeremonyStep.StepSwapKey || !!c.choose;
  // Nothing follows Done in the same ceremony: an event after it opens
  // the next enrolment, from its first half.
  return { seq: c.seq, phase: turned ? 1 : prev.phase === PAST ? 0 : prev.phase };
}

// Tone is the retries' ink in the PIN's label row: the second ink at
// three or more, attention at two, danger at one (APP.md §6, ruled
// 2026-10-17). A verified card cannot count, and says so plainly.
export type Tone = "" | "attention" | "danger";

export function retriesTone(s: { retries: number; retriesKnown: boolean; verified: boolean }): Tone {
  if (!s.retriesKnown || s.verified || s.retries <= 0) return "";
  return s.retries === 1 ? "danger" : s.retries === 2 ? "attention" : "";
}

// lastTry: the next wrong PIN blocks the YubiKey's PIN, so the panel says
// it above the field, with the way out beside it.
export function lastTry(s: { retries: number; retriesKnown: boolean; verified: boolean }): boolean {
  return retriesTone(s) === "danger";
}

// ---- The ceremony in a dialog (APP.md §6, §13; ruled 2026-10-17) ---------
// The Vault page's acts — Add an unlock method, Remove, Re-key, Show the
// recovery key, Export a backup, the password with every YubiKey — and
// the proof of Import archives from a backup… run the lock screen's
// ceremony inside their dialog, drawn by the same views.

// dialogWay: the way a dialog's ceremony proves it is you. The core's own
// Method once it has named it; before that, the step's evidence; and with
// none, the YubiKey where the vault has one — the core proves with a
// YubiKey whenever the vault has one (keys.go acquireUnlocked), so the
// opening wait is drawn as the YubiKey's at once rather than flashing a
// quiet panel first.
export function dialogWay(method: string, hasHardware: boolean, c: { kind: string; step: CeremonyStep } | null | undefined): Way {
  const w = wayOf(method, "", c);
  if (w) return w;
  return hasHardware ? "token" : "";
}

// typedProofLeads: a dialog's typed proof heads with what to type and why
// (ceremonyCopy.typeProve), as the YubiKey's wait heads with what to
// insert — only the proof itself, not a password being chosen nor an
// enrolment's second half, whose line names the step (APP.md §13, ruled
// 2026-10-17).
export function typedProofLeads(f: { way: Way; shows: string; choose?: boolean; second?: boolean }): boolean {
  return f.way === "password" && f.shows === "password" && !f.choose && !f.second;
}

// FootButton is one of a dialog ceremony's foot-band buttons, in their
// order: the act first, the way out after it.
export type FootButton = "continue" | "again" | "cancel" | "close";

// dialogFoot: what a dialog ceremony's foot band holds (APP.md §6,
// Controls; ruled 2026-10-17). Continue beside Cancel at a prompt; Cancel
// alone while nothing is asked. Over, a failure the user did not choose —
// the YubiKey stopped waiting, a refusal — offers Start again beside
// Cancel, as the lock screen's failure does, where the page can begin the
// act again; anything else over has Close alone.
export function dialogFoot(f: { over: boolean; view: View; asks: boolean; again: boolean }): FootButton[] {
  if (f.over) return f.view === "failed" && f.again ? ["again", "cancel"] : ["close"];
  return f.asks ? ["continue", "cancel"] : ["cancel"];
}

export interface DialogFacts {
  c: { kind: string; step: CeremonyStep; error?: string; promptId?: string; removeLabel?: string } | null | undefined;
  way: Way;
  // passed, afterTouch: as StageFacts' (passedAfter).
  passed?: boolean;
  afterTouch?: boolean;
}

// dialogViewOf is the view a dialog's panel asks for: the lock screen's
// (viewOf), the dialog standing open for as long as the ceremony is on it,
// with two differences. An enrolment's swap — the YubiKey that proved it
// is you to come out — has a view of its own, the key art's. And the
// green is only ever a touch's (ruled 2026-10-17): the derivation, the
// release and Done stay on it when a touch is behind them with nothing
// asked since — the proof's touch, or the new YubiKey's in an enrolment's
// second half (keys.go proveKey touches it) — and are drawn on the
// neutral panel otherwise: after a typed proof, or after a prompt that
// came between the proof's touch and the end (a new password chosen, a
// recovery key shown), where the green would arrive only to leave.
export function dialogViewOf(f: DialogFacts): View {
  const c = f.c;
  const v = viewOf({ running: true, firstRun: false, way: f.way, c, passed: f.passed, afterTouch: f.afterTouch });
  if (!c) return v;
  if (v === "wait" && c.step === CeremonyStep.StepSwapKey && c.removeLabel) return "swap";
  if (v === "touch" && c.step !== CeremonyStep.StepTouch && !f.afterTouch) return c.step === CeremonyStep.StepDone ? "done" : "opening";
  return v;
}

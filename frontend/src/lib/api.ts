// The bound services and their models, re-exported from the generated
// bindings, plus the raw channel for the four typed secrets and the
// replacement's two answers (APP.md §1).
import { System } from "@wailsio/runtime";
import { Vault, Archives, Archive, Keys, Settings, Shell } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app/api";
import type { CeremonyStep, Code } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";

export { Vault, Archives, Archive, Keys, Settings, Shell };
export * from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";
// The Settings service shadows the Settings model above; the model is SettingsView.
export type { Settings as SettingsView } from "../../bindings/github.com/dreamxwarden01/enfold/internal/app";

// StepKey and CodeKey are the enums without the generator's $zero member.
export type StepKey = Exclude<CeremonyStep, CeremonyStep.$zero>;
export type CodeKey = Exclude<Code, Code.$zero>;

// AppError is the one error shape a service returns (app.Error).
export interface AppError {
  code: string;
  retries?: number;
  slot?: string;
  // archive.open_elsewhere: the file the archive is already open from
  // (APP.md §14).
  path?: string;
}

// errorOf reads the coded error out of a rejected call. Wails attaches the
// marshalled error as `cause`; the message is the code itself.
export function errorOf(e: unknown): AppError {
  if (e && typeof e === "object") {
    const cause = (e as { cause?: unknown }).cause;
    if (cause && typeof cause === "object" && typeof (cause as AppError).code === "string") {
      return cause as AppError;
    }
    const msg = (e as { message?: unknown }).message;
    if (typeof msg === "string" && /^[a-z_.]+$/.test(msg)) {
      return { code: msg };
    }
  }
  return { code: "internal" };
}

// submitSecret hands a PIN, password, recovery digits or management key to
// the waiting prompt over the raw message channel, which Wails does not
// log. The value is never an argument of a bound call. A replacement's
// "kept" (a new recovery key is kept) and "confirm" (its one confirmation)
// go the same way with an empty value: the answer is that the prompt with
// this id may go on, which is what the mailbox is, and its id and kind
// must match, so neither can answer a PIN's prompt (ruled 2026-10-27).
export function submitSecret(kind: "pin" | "password" | "recovery" | "mgmtkey" | "kept" | "confirm", promptId: string, value: string): void {
  System.invoke(`secret ${kind} ${promptId} ${value}`);
}

// stageGreen tells the shell whether the touch's green is up on the lock
// stage (APP.md §2.4, §6; ruled 2026-11-03), so that the window's caption
// takes the green with the page and gives it back with it: the page is
// what knows when the green is up. It is said as the page's own change
// begins, with how long that change takes in ms and the curve it runs on
// as CSS writes it, so that the shell eases the caption on them; 0 ms is
// a change the page draws at once. Nothing of the ceremony. Only a change
// of the green is sent, whatever its duration — a word repeated with
// another length moves nothing, since the green is where it was — and the
// first word of a page always: a page loaded again mid-ceremony says what
// it shows. Each call waits for the one before it to answer, since the
// shell takes every bound call on a goroutine of its own and two in
// flight could land in either order; the shell answers as it starts a
// fade, not as it ends one. A refusal, or a page with no shell behind it
// (the mock answers null), is nothing to the page: the caption is the
// shell's to keep.
let greenSaid: boolean | null = null;
let greenCalls: Promise<unknown> = Promise.resolve();
export function stageGreen(on: boolean, ms: number, curve: string): void {
  if (greenSaid === on) return;
  greenSaid = on;
  greenCalls = greenCalls.then(() => Shell.StageGreen(on, ms, curve)).catch(() => undefined);
}

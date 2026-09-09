// Field validation (APP.md §6, Forms): what a value is missing, said the
// way the user reads it. Empty strings mean the value is fine.
export type SecretKind = "pin" | "password" | "recovery" | "mgmtkey";

export const MIN_PASSWORD = 8;
export const REQUIRED = "This field is required.";
export const PASSWORD_RULE = `At least ${MIN_PASSWORD} characters.`;
export const PIN_RULE = "Up to 8 characters.";
export const RECOVERY_RULE = "48 digits, in groups or not.";
export const MGMT_RULE = "32, 48 or 64 hex digits.";

// The separators a recovery key may carry: any whitespace, and every
// dash a word processor may have put in for '-' — the same set the core
// ignores (kdf.ParseRecoveryDigits).
export const RECOVERY_SEPARATORS = /[\s\-\u2010\u2011\u2012\u2013\u2014\u2212]/g;
// The whitespace a management key may carry: what the core ignores
// (decodeHexKey), no more.
export const MGMT_SEPARATORS = /[ \t\r\n]/g;

// secretProblem judges a typed secret before it is sent. choose: the
// secret is being chosen now, so the minimum applies (an existing one is
// never measured — it is what it is).
export function secretProblem(kind: SecretKind, value: string, choose = false): string {
  if (!value) return REQUIRED;
  switch (kind) {
    case "password":
      return choose && [...value].length < MIN_PASSWORD ? PASSWORD_RULE : "";
    case "pin":
      // An existing secret: only what the card itself refuses (more than
      // 8 bytes) is refused here; a shorter PIN is what the key was given.
      return new TextEncoder().encode(value).length > 8 ? PIN_RULE : "";
    case "recovery": {
      const digits = value.replace(RECOVERY_SEPARATORS, "");
      return /^\d{48}$/.test(digits) ? "" : RECOVERY_RULE;
    }
    case "mgmtkey": {
      const hex = value.replace(MGMT_SEPARATORS, "");
      return /^[0-9a-fA-F]+$/.test(hex) && [32, 48, 64].includes(hex.length) ? "" : MGMT_RULE;
    }
  }
  return "";
}

// autoSendable judges the one secret the page sends without a click: the
// password prompt an accepted PIN brings, answered from the field as the
// user left it (APP.md §6, the ruling of 2026-09-08). It is judged by the
// same rule the button applies, so a field the form would refuse — an
// empty one, cleared in the moment between the press and the prompt — is
// marked and waited on rather than sent: every typed secret is judged
// before it is sent, this one included.
export function autoSendable(kind: SecretKind, value: string, choose = false): boolean {
  return !secretProblem(kind, value, choose);
}

// rule is the static requirement shown under a field, when there is one.
export function secretRule(kind: SecretKind, choose = false): string {
  switch (kind) {
    case "password":
      return choose ? PASSWORD_RULE : "";
    case "pin":
      return PIN_RULE;
    case "recovery":
      return RECOVERY_RULE;
    case "mgmtkey":
      return MGMT_RULE;
  }
  return "";
}

export function requiredProblem(value: string): string {
  return value.trim() ? "" : REQUIRED;
}

// An archive's description is bounded in BYTES of UTF-8, not characters
// (FORMAT.md §7.1): 1 024 ASCII letters fit, 400 four-byte emoji do not
// although they are 400 characters. Empty clears the description and is
// always fine.
export const DESCRIPTION_MAX = 1024;
export const DESCRIPTION_RULE = "At most 1 024 bytes — fewer characters with accents or emoji.";
export function descriptionProblem(text: string): string {
  return new TextEncoder().encode(text).length > DESCRIPTION_MAX ? DESCRIPTION_RULE : "";
}

// confirmNameProblem judges the archive's name typed to confirm a Forget
// or a Delete (APP.md §13): compared trimmed and exactly, case and all —
// the typed name is the brake, so a near miss is a miss.
export const CONFIRM_NAME = "Type the archive's name exactly as it is shown.";
export function confirmNameProblem(typed: string, name: string): string {
  if (!typed.trim()) return REQUIRED;
  return typed.trim() === name.trim() ? "" : CONFIRM_NAME;
}

// An archive's name is bounded app-side only, at the same 1 024 bytes as
// the description, and is never empty (APP.md §13, the ruling of
// 2026-09-07): the confirmation for Forget and Delete is the name typed,
// so an unbounded name would defeat its own brake. FORMAT.md §7.1 puts no
// rule on the wire, and the core answers archive.name_invalid.
export const NAME_MAX = 1024;
export const NAME_RULE = "A name is required, at most 1 024 bytes.";
export function nameProblem(text: string): string {
  if (!text.trim()) return REQUIRED;
  return new TextEncoder().encode(text).length > NAME_MAX ? NAME_RULE : "";
}

// confirmSecretProblem judges the second field of a chosen secret: one
// core prompt, two fields on the page, one submission (APP.md §13).
export const CONFIRM_SECRET = "The two do not match.";
export function confirmSecretProblem(a: string, b: string): string {
  if (!b) return REQUIRED;
  return a === b ? "" : CONFIRM_SECRET;
}

// The recovery key's eight groups (RecoveryInput): each carries 16 bits
// as a six-digit number divisible by 11 and below 720 896 — BitLocker's
// checksum, which catches a mistyped group as it is finished. Empty means
// the group is fine, or not finished yet.
export const RECOVERY_GROUPS = 8;
export const RECOVERY_GROUP_LEN = 6;
export const RECOVERY_GROUP_MAX = 720896;
export const GROUP_MISTYPED = "This group is mistyped.";
export function recoveryGroupProblem(group: string): string {
  if (group.length < RECOVERY_GROUP_LEN) return "";
  if (!/^\d{6}$/.test(group)) return GROUP_MISTYPED;
  const n = Number(group);
  return n < RECOVERY_GROUP_MAX && n % 11 === 0 ? "" : GROUP_MISTYPED;
}


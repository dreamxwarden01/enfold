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

// The tiers of a notice (direction 9 of the design review, ruled
// 2026-10-18): three fixed meanings, each with its colour and its icon,
// so a possible attack, a weakened guarantee and a self-repair no longer
// wear one amber. The tier sets how a notice looks; the state sets where
// it stands (on the lock screen, above the panel: APP.md §6).
//
// - danger: the vault file may have been changed outside Enfold, and
//   every change to it is refused until a trusted copy replaces it
//   (APP.md §13, the Tampered state). Red, the error circle.
// - attention: a guarantee is weaker than it should be — the YubiKey may
//   still be PIN-verified, lock detection is polling, the settings could
//   not be written. Amber, the triangle.
// - info: heals itself or only informs — a damaged header copy the next
//   change repairs, stored timeouts put back to the defaults. The plain
//   bar (--surface-alt, the second ink), the info circle; never amber.
//
// A code nobody has classified is attention: a warning is worth a look
// until someone has said it is not.
import { Code } from "./api";

export type Tier = "danger" | "attention" | "info";

// OPENED stands for the lock screen's own notice that archives the lock
// left open are still decrypted and still save (APP.md §2.3): not a code
// the core raises, but ranked among them, as attention.
export const OPENED = "ui.archives_open";

const DANGER = new Set<string>([Code.CodeVaultTampered, Code.CodeTamperedHash, Code.CodeTamperedGeneration]);
const INFO = new Set<string>([Code.CodeVaultStale, "vault.timeouts_clamped"]);

export function tier(code: string): Tier {
  if (DANGER.has(code)) return "danger";
  if (INFO.has(code)) return "info";
  return "attention"; // OPENED, the attention codes, and every unclassified one
}

// The icon each tier wears (Icons.svelte): the error circle, the
// triangle, the info circle.
export function tierIcon(t: Tier): string {
  return t === "danger" ? "i-error" : t === "attention" ? "i-warn" : "i-info";
}

const RANK: Record<Tier, number> = { danger: 0, attention: 1, info: 2 };

// ranked puts the warnings in the order they are drawn: danger first, then
// attention, then info, and within a tier as the core listed them.
export function ranked(codes: readonly string[]): string[] {
  return codes
    .map((code, i) => ({ code, i }))
    .sort((a, b) => RANK[tier(a.code)] - RANK[tier(b.code)] || a.i - b.i)
    .map((x) => x.code);
}

// tamperedShown is the one predicate every surface draws the Tampered
// banner from (APP.md §6, The Tampered banner): the lock screen, Archives,
// an archive's page, Vault and Settings. The core's two facts disagree
// after a lock — the lock clears VaultStatus.tampered and its reason but
// keeps the vault.tampered warning until a clean unlock or a replace
// (internal/app/session.go lockLocked) — so either one shows the banner,
// and the reason, empty after a lock, is the banner's Why? to word.
export function tamperedShown(st: { tampered?: boolean; warnings?: readonly string[] | null } | null | undefined): boolean {
  if (!st) return false;
  return !!st.tampered || (st.warnings ?? []).some((w) => tier(w) === "danger");
}

// lockNotices is the lock screen's list, ranked: the Tampered banner once
// (whichever danger code or fact raised it), then the core's other
// warnings and the open-archives notice by tier.
export function lockNotices(warnings: readonly string[], tampered: boolean, opened: boolean): string[] {
  const rest = warnings.filter((w) => tier(w) !== "danger");
  return ranked([...(tampered ? [Code.CodeVaultTampered as string] : []), ...rest, ...(opened ? [OPENED] : [])]);
}

// What the first-run card is about (APP.md §2.1, §6): a vault to create or
// import, a configured file that is absent, or one that is there and not a
// keystore — the one case a rebuild is offered for. Null when a vault is
// kept, which is never joined by a second one.
import type { VaultStatus } from "./api";
import { VaultState } from "./api";

export type FirstRunCard = "fresh" | "absent" | "damaged";

export function firstRunCard(st: VaultStatus | null | undefined): FirstRunCard | null {
  if (!st || st.state !== VaultState.StateNone) return null;
  if (!st.missingPath) return "fresh";
  return st.damaged ? "damaged" : "absent";
}

// retiredAt is when a vault was retired as a dated copy, from its file's
// name — vault-replaced-<unix>-<n>.eks (internal/app/vaultfile.go) — so
// the damaged vault's panel can date the copy it offers first (APP.md §6,
// ruled 2026-10-17). 0 when the name carries no date.
export function retiredAt(path: string): number {
  const m = /vault-replaced-(\d+)-\d+\.eks$/i.exec(path);
  return m ? Number(m[1]) : 0;
}

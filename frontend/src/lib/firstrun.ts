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
